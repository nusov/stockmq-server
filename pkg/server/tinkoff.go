package server

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"sync"
	"syscall"

	"github.com/tinkoff/invest-api-go-sdk/investgo"
	pb "github.com/tinkoff/invest-api-go-sdk/proto"
)

type TinkoffLogger struct {
}

func (c *TinkoffLogger) Infof(template string, args ...any) {

}

func (c *TinkoffLogger) Errorf(template string, args ...any) {

}

func (c *TinkoffLogger) Fatalf(template string, args ...any) {

}

// Start Tinkoff client
func (s *Server) startTinkoff() {
	// загружаем конфигурацию для сдк из .yaml файла
	config, err := investgo.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("config loading error %v", err.Error())
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	defer cancel()

	// сдк использует для внутреннего логирования investgo.Logger
	logger := &TinkoffLogger{}

	// создаем клиента для investAPI, он позволяет создавать нужные сервисы и уже
	// через них вызывать нужные методы
	client, err := investgo.NewClient(ctx, config, logger)
	if err != nil {
		logger.Fatalf("client creating error %v", err.Error())
	}
	defer func() {
		logger.Infof("closing client connection")
		err := client.Stop()
		if err != nil {
			s.Errorf("client shutdown error %v", err.Error())
		}
	}()

	// для синхронизации всех горутин
	wg := &sync.WaitGroup{}

	// один раз создаем клиента для стримов
	MDClient := client.NewMarketDataStreamClient()

	// создаем стримов сколько нужно, например 2
	firstMDStream, err := MDClient.MarketDataStream()
	if err != nil {
		s.Errorf(err.Error())
	}
	// результат подписки на инструменты это канал с определенным типом информации, при повторном вызове функции
	// подписки(например на свечи), возвращаемый канал можно игнорировать, так как при первом вызове он уже был получен
	firstInstrumetsGroup := []string{"BBG004730N88", "BBG00475KKY8", "BBG004RVFCY3"}
	candleChan, err := firstMDStream.SubscribeCandle(firstInstrumetsGroup, pb.SubscriptionInterval_SUBSCRIPTION_INTERVAL_ONE_MINUTE, true)
	if err != nil {
		s.Errorf(err.Error())
	}

	tradesChan, err := firstMDStream.SubscribeTrade(firstInstrumetsGroup)
	if err != nil {
		s.Errorf(err.Error())
	}

	// функцию Listen нужно вызвать один раз для каждого стрима и в отдельной горутине
	// для остановки стрима можно использовать метод Stop, он отменяет контекст внутри стрима
	// после вызова Stop закрываются каналы и завершается функция Listen
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := firstMDStream.Listen()
		if err != nil {
			s.Errorf(err.Error())
		}
	}()

	// для дальнейшей обработки, поступившей из канала, информации хорошо подойдет механизм,
	// основанный на паттерне pipeline https://go.dev/blog/pipelines

	wg.Add(1)
	go func(ctx context.Context) {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				logger.Infof("stop listening first channels")
				return
			case candle, ok := <-candleChan:
				if !ok {
					return
				}
				// клиентская логика обработки...
				fmt.Println("high price = ", candle.GetHigh().ToFloat())
			case trade, ok := <-tradesChan:
				if !ok {
					return
				}
				// клиентская логика обработки...
				fmt.Println("trade price = ", trade.GetPrice().ToFloat())
			}
		}
	}(ctx)

	// Для еще одного стрима в этом grpc.conn //
	secondMDStream, err := MDClient.MarketDataStream()
	if err != nil {
		s.Errorf(err.Error())
	}

	// доступные значения глубины стакана: 1, 10, 20, 30, 40, 50
	secondInstrumetsGroup := []string{"BBG004S681W1", "BBG004731354"}
	obChan, err := secondMDStream.SubscribeOrderBook(secondInstrumetsGroup, 10)
	if err != nil {
		s.Errorf(err.Error())
	}

	lastPriceChan, err := secondMDStream.SubscribeLastPrice(secondInstrumetsGroup)
	if err != nil {
		s.Errorf(err.Error())
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := secondMDStream.Listen()
		if err != nil {
			s.Errorf(err.Error())
		}
	}()

	wg.Add(1)
	go func(ctx context.Context) {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				logger.Infof("stop listening second channels")
				return
			case ob, ok := <-obChan:
				if !ok {
					return
				}
				fmt.Println("order book time is = ", ob.GetTime().AsTime().String())
			case lp, ok := <-lastPriceChan:
				if !ok {
					return
				}
				fmt.Println("last price  = ", lp.GetPrice().ToFloat())
			}
		}
	}(ctx)

	wg.Wait()
}
