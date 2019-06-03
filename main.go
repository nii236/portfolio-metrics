package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/go-chi/chi"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var portfolioTotal *atomic.Value

// PriceAPIURL is the API endpoint for pricing data
const PriceAPIURL = "https://min-api.cryptocompare.com/data/pricemulti"

// Config is the config from the TOML file
type Config struct {
	BindAddress string       `toml:"BindAddress"`
	Currency    string       `toml:"Currency"`
	Coins       []CoinConfig `toml:"Coins"`
}

// CoinConfig is the sub-config from the TOML file
type CoinConfig struct {
	Name   string  `toml:"Name"`
	Amount float64 `toml:"Amount"`
}

func main() {
	portfolioTotal = &atomic.Value{}
	config, err := ParseConfig()
	if err != nil {
		fmt.Println(err)
		return
	}

	coins := GetCoins(config)
	gauges := PrepareGauges(coins, config.Currency)
	UpdatePortfolio(config, coins, config.Currency, gauges)
	StartSubscription(config, coins, config.Currency, gauges)
	r := chi.NewRouter()
	r.Handle("/metrics", promhttp.Handler())
	r.Get("/", GetPortfolio(gauges))
	fmt.Println("Starting on", config.BindAddress)
	log.Fatalln(http.ListenAndServe(config.BindAddress, r))
}

// GetPortfolio returns the total value of the portfolio
func GetPortfolio(gauges map[string]prometheus.Gauge) http.HandlerFunc {
	fn := func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf("%.2f", portfolioTotal.Load())))
	}

	return fn
}

// StartSubscription will update the portfolio every minute
func StartSubscription(config *Config, coins []string, currency string, gauges map[string]prometheus.Gauge) {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for {
			select {
			case <-ticker.C:
				UpdatePortfolio(config, coins, currency, gauges)
			}
		}
	}()
}

// PrepareGauges iterates over the crypto symbols and adds them to the prometheus metrics
func PrepareGauges(coins []string, currency string) map[string]prometheus.Gauge {
	gauges := map[string]prometheus.Gauge{}
	for _, coin := range coins {
		symbol := strings.ToLower(coin)
		gauge := prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "portfolio_metrics",
			Subsystem: symbol,
			Name:      strings.ToLower(currency),
			Help:      "Ticker for a specific crypto",
		})
		prometheus.Register(gauge)
		gauges[symbol] = gauge
	}
	return gauges
}

// ParseConfig will parse config.toml into a struct
func ParseConfig() (*Config, error) {
	conf := &Config{}
	_, err := toml.DecodeFile("config.toml", conf)
	if err != nil {
		return nil, err
	}

	return conf, nil
}

// GetCoins iterates over the config to get the list of coins
func GetCoins(conf *Config) []string {
	coins := []string{}
	for _, coin := range conf.Coins {
		coins = append(coins, coin.Name)
	}
	return coins
}

// UpdatePortfolio will iterate over the coins and call the API getter func
func UpdatePortfolio(config *Config, coins []string, currency string, gauges map[string]prometheus.Gauge) {
	fmt.Println("Updating portfolio...")
	prices, err := GetPrices(coins, currency)
	if err != nil {
		fmt.Println(err)
		return
	}
	total := 0.0
	for tsym, psyms := range prices {
		symbol := strings.ToLower(tsym)
		for pName, psym := range psyms {
			if strings.ToLower(pName) == strings.ToLower(currency) {
				subtotal := psym * GetAmount(config, tsym)
				gauges[symbol].Set(total)
				total = total + subtotal
			}
		}
	}
	portfolioTotal.Store(total)

}

// GetAmount pulls the amount for a specific coin
func GetAmount(config *Config, tsym string) float64 {
	for _, coin := range config.Coins {
		if coin.Name == tsym {
			return coin.Amount
		}
	}
	return 0
}

// GetPrices does the actual request to the API
func GetPrices(coins []string, currency string) (PriceAPIResponse, error) {
	u, err := url.Parse(PriceAPIURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("fsyms", strings.Join(coins, ","))
	q.Set("tsyms", currency)
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, err
	}
	if resp.StatusCode > 299 {
		return nil, errors.New("Bad status: " + resp.Status)
	}

	result := PriceAPIResponse{}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	err = json.Unmarshal(b, &result)
	if err != nil {
		return nil, err
	}

	return result, nil

}

// PriceAPIResponse is the JSON response from the API
type PriceAPIResponse map[string]Tickers

// Tickers is the JSON response from the API
type Tickers map[string]float64

// Tick is the JSON response from the API
type Tick float64
