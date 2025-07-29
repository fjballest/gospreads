package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"github.com/sklinkert/igmarkets"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"path/filepath"
	"os"
	"time"
	"runtime"
	"os/exec"
)

const kFilePath = "ig.txt"
const suff = ".MINI.IP"

var (
	key string
	acid string
	uid string
	pw  string
)

type ExchangeResponse struct {
	Result string             `json:"result"`
	Base   string             `json:"base_code"`
	Rates  map[string]float64 `json:"rates"`
}

type Forex struct {
	Name     string
	Epic     string
	Bid      float64
	Ask      float64
	Spread   float64
	Currency string
	Pip      float64
	PipVal   float64
	CurrEurs float64
}

type In struct {
	Name	string
	Risk	float64
	Stop	float64
}

type Out struct {
	Forex
	Err    string
	SpreadEur float64
	Stop	float64
	StopCurr float64
	StopEur float64
	Risk	float64
	RiskCurr float64
	Lots   float64
	Result bool
}

type IG struct {
	Ctx context.Context
	Ig  *igmarkets.IGMarkets
}

type Svc struct {
	In
	ig *IG
	tmpl, tmple *template.Template
	w http.ResponseWriter
	r *http.Request
}

var Pairs = []string{
	"CS.D.EURUSD"+suff,
}

func xDir() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "."
	}
	abs = filepath.Clean(abs)
	d := filepath.Dir(abs)
	return d
}

func kFile() string {
	d := xDir()
	return filepath.Join(d, kFilePath)
}

func kFileKeys() bool {
	p := kFile()
	log.Println(p)
	txt, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	lines := strings.Split(string(txt), "\n")
	log.Println(lines)
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		els := strings.Fields(ln)
		if len(els) < 2 {
			continue
		}
		if els[0] == "IGKEY" {
			key = strings.TrimSpace(els[1])
		} else if els[0] == "IGACCOUNT" {
			acid = strings.TrimSpace(els[1])
		} else if els[0] == "IGUID" {
			uid = strings.TrimSpace(els[1])
		} else if els[0] == "IGPASS" {
			pw = strings.TrimSpace(els[1])
		}
	}
	return key != "" || acid != "" || uid != "" || pw != ""
}

func setKeys() {
	kFileKeys()
	if k := os.Getenv("IGKEY"); k != "" {
		key = k
	}
	if k := os.Getenv("IGACCOUNT"); k != "" {
		acid = k
	}
	if k := os.Getenv("IGUID"); k != ""  {
		uid = k
	}
	if k := os.Getenv("IGPASS"); k != ""  {
		pw = k
	}
	if key == "" || acid == "" || uid == "" || pw == "" {
		log.Fatal("set variables for IGKEY IGACCOUNT IGUID IGPASS and retry")
	}
}

func init() {
	spew.Config.Indent = "\t"
}

func (ig *IG) Get(name string) (*Forex, error) {
	instr, _, err := ig.GetInstrument(name)
	if err != nil {
		return nil, err
	}
	return ig.InstrForex(instr)
}

func (ig *IG) GetInstrument(name string) (*igmarkets.Instrument, *igmarkets.Snapshot, error) {
	m, err := ig.Ig.GetMarkets(ig.Ctx, name)
	if err != nil {
		return nil, nil, err
	}
	spew.Dump(m)
	instr := m.Instrument
	snap := m.Snapshot
	// NB: the prices in the snap are NOT the
	// prices on PRT. Those are found in ig.Ig.GetPrice()
	return &instr, &snap, nil
}

func (ig *IG) InstrForex(instr *igmarkets.Instrument) (*Forex, error) {
	cur := ""
	pip := float64(1.0)
	pipval := float64(1.0)
	rate := 0.0
	var err error
	if len(instr.Currencies) > 0 {
		c := instr.Currencies[0]
		cur = c.Code
		pipval, err = strconv.ParseFloat(instr.ValueOfOnePip, 64)
		if err != nil {
			pipval = 1.0
		}
		rate = c.ExchangeRate
		ps := instr.OnePipMeans
		if ps != "" {
			els := strings.Fields(ps)
			pip, err = strconv.ParseFloat(els[0], 64)
			if err != nil {
				pip = 1.0
			}
		}
	}
	name := instr.ChartCode
	epic := instr.Epic
	prices, err := ig.Ig.GetPrice(ig.Ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("GetPrice: %v", err)
	}
	if len(prices.Prices) < 0 {
		return nil, fmt.Errorf("GetPrice: no prices")
	}
	bid := float64(0.0)
	ask := bid
	// the prices are 11641.7 for an EURUSD of 1.116417
	// with a pip size of 0.0001
	for _, pr := range(prices.Prices) {
		c := pr.ClosePrice
		if bid == 0 || (c.Bid > 0 && c.Bid < bid) {
			bid = c.Bid
		}
		if ask == 0 || (c.Ask > 0 && c.Ask > ask) {
			ask = c.Ask
		}
	}
	ceurs := rate
	if ceurs <= 0 {
		// use this to rely on an external curr. converter
		ceurs, err = Convert(cur, "EUR", 1.0)
		if err != nil {
			return nil, err
		}
	}
	spread := ask - bid
	ask *= pip
	bid *= pip
	return &Forex{
		Name:     name,
		Epic:     epic,
		Bid:      bid,
		Ask:      ask,
		Spread:   spread,
		Currency: cur,
		Pip:      pip,
		PipVal:   pipval,
		CurrEurs: ceurs,
	}, nil
}

func (ig *IG) List(keys ...string) ([]string, error) {
	var epics []string
	ks := make(map[string]bool)
	for _, k := range keys {
		mr, err := ig.Ig.MarketSearch(ig.Ctx, k)
		if err != nil {
			return nil, fmt.Errorf("Search: %v", err)
		}
		for _, m := range mr.Markets {
			e := m.Epic
			if false && strings.HasSuffix(e, "OPTCALL.IP") {
				e = e[:len(e)-len("OPTCALL.IP")] + suff
			}
			if false && strings.HasSuffix(e, "OPTPUT.IP") {
				e = e[:len(e)-len("OPTPUT.IP")] + suff
			}
			if _, ok := ks[e]; !ok {
				epics = append(epics, e)
				ks[e] = true
			}
		}
	}
	return epics, nil
}

func getRates(base string) (map[string]float64, error) {
	url := fmt.Sprintf("https://open.er-api.com/v6/latest/%s", base)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch exchange rates: %v", err)
	}
	defer resp.Body.Close()

	var data ExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}

	if data.Result != "success" {
		return nil, fmt.Errorf("API returned error result: %s", data.Result)
	}

	return data.Rates, nil
}

func Convert(base, target string, amount float64) (float64, error) {
	rates, err := getRates(base)
	if err != nil {
		return 0, err
	}

	rate, ok := rates[target]
	if !ok {
		return 0, fmt.Errorf("target currency %s not found", target)
	}

	return amount * rate, nil
}

func New() (*IG, error) {
	setKeys()
	var ctx = context.Background()
	ig := igmarkets.New(igmarkets.DemoAPIURL, key, acid, uid, pw)
	if err := ig.Login(ctx); err != nil {
		return nil, fmt.Errorf("login: %v", err)
	}
	return &IG{ctx, ig}, nil
}

func list(ig *IG) {
	l, _ := ig.List("EUR", "USD", "GBP")
	for _, x := range l {
		fmt.Println(x)
	}
}

func tryEur(ig *IG) {
	m, err := ig.Ig.GetMarkets(ig.Ctx, "CS.D.EURUSD.MINI.IP")
	if err != nil {
		log.Fatalf("markets %v", err)
	}
	spew.Dump(m)
	prices, err := ig.Ig.GetPrice(ig.Ctx, "CS.D.EURUSD.MINI.IP")
	if err != nil {
		log.Fatalf("prices %v", err)
	}
	spew.Dump(prices)
}

var server *http.Server

func main() {
	ig, err := New()
	if err != nil {
		log.Fatalf("ig: %v", err)
	}
	if false {
		tryEur(ig)
		return
	}
	svc := &Svc {
		ig: ig,
		In: In {
			Name: "EURUSD",
			Risk: 100,
			Stop: 10,
		},
		tmpl: template.Must(template.New("forexStops").Parse(page)),
		tmple: template.Must(template.New("forexStops").Parse(pageerr)),
	}
	h := func(w http.ResponseWriter, r *http.Request) {
		svc.handler(w, r)
	}
	mux := http.NewServeMux()
	server = &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}
	mux.HandleFunc("/", h)
	mux.HandleFunc("/exit", hexit)
	fmt.Println("Starting web server on http://localhost:8080")
	go func() {
		time.Sleep(2*time.Second)
		browse()
	}()
	server.ListenAndServe()
	// list(ig)
}

func hexit(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
			fmt.Fprintln(w, "Bye")
			go func() {
				time.Sleep(1 * time.Second)
				server.Close()
				os.Exit(0)
			}()
		} else {
			http.Redirect(w, r, "/", http.StatusSeeOther)
		}
}


const page = `
<html>
	<head>
		<title>Forex Stops and Lots</title>
		<style>
			body { font-family: Arial, sans-serif; background-color: #f7f7f7; }
			h1 { color: #333; }
			form { background-color: #fff; padding: 20px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1); }
			input[type="text"], input[type="number"] { padding: 10px; margin: 5px 0 15px 0; width: 200px; border-radius: 4px; border: 1px solid #ddd; }
			button[type="submit"] { padding: 10px 20px; background-color: #28a745; color: white; border: none; border-radius: 4px; cursor: pointer; }
			input[type="submit"] { padding: 10px 20px; background-color: #28a745; color: white; border: none; border-radius: 4px; cursor: pointer; }
			input[type="submit"]:hover { background-color: #218838; }
			.result { background-color: #fff; padding: 20px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1); }
			.result h2 { color: #333; }
		</style>
	</head>
	<body>
		<h1>Forex Stops</h1>
		{{if .Result}}
		<form method="POST">
			<label for="ticker">Ticker (eg EURUSD):</label>
			<input type="text" name="ticker" id="ticker" value="{{.Name}}"><br>

			<label for="risk">Riesgo Max (Eurs):</label>
			<input type="text" name="risk" id="risk" value="{{.Risk}}"><br>

			<label for="stop">Stop (pips):</label>
			<input type="text" name="stop" id="stop" value="{{.Stop}}"><br>

			<input type="submit" value="Go">
			<button type="submit" formaction="/exit" formmethod="POST">Exit</button>
		</form>

		<div class="result">
			<h2>Ok:</h2>
			<p><strong>EPIC:</strong> {{.Epic}}</p>
			<p><strong>Pip:</strong> {{.Pip}} pt / {{printf "%f" .PipVal}} {{.Currency}}</p>
			<p><strong>Bid:</strong> {{printf "%f" .Bid}} <strong>Ask:</strong> {{printf "%f" .Ask}}</p>
			<p><strong>Spread:</strong> {{printf "%f" .Spread}} pips  {{printf "%f" .SpreadEur}} EUR</p>
			<p><strong>Stop:</strong> {{printf "%.3f" .StopCurr}} {{.Currency}} = {{printf "%.3f" .StopEur}} EUR</p>
			<p><strong>Riesgo:</strong> {{printf "%.2f" .RiskCurr}} {{.Currency}} = {{.Risk}} EUR</p>
			<p><strong>Lotes:</strong> {{printf "%.2f" .Lots}}</p>
			<p><strong>Cambio:</strong> 1 {{.Currency}} = {{printf "%.4f" .CurrEurs}} EUR</p>
		</div>
		{{end}}
	</body>
</html>`

const pageerr = `
<html>
	<head>
		<title>Forex Stops and Lots</title>
		<style>
			body { font-family: Arial, sans-serif; background-color: #f7f7f7; }
			h1 { color: #333; }
			form { background-color: #fff; padding: 20px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1); }
			input[type="text"], input[type="number"] { padding: 10px; margin: 5px 0 15px 0; width: 200px; border-radius: 4px; border: 1px solid #ddd; }
			button[type="submit"] { padding: 10px 20px; background-color: #28a745; color: white; border: none; border-radius: 4px; cursor: pointer; }
			input[type="submit"] { padding: 10px 20px; background-color: #28a745; color: white; border: none; border-radius: 4px; cursor: pointer; }
			input[type="submit"]:hover { background-color: #218838; }
			.result { background-color: #fff; padding: 20px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1); }
			.result h2 { color: #333; }
		</style>
	</head>
	<body>
		<h1>Forex Stops</h1>
		<form method="POST">
			<label for="ticker">Ticker (eg EURUSD):</label>
			<input type="text" name="ticker" id="ticker" value="{{.Name}}"><br>

			<label for="risk">Riesgo Max (Eurs):</label>
			<input type="text" name="risk" id="risk" value="{{.Risk}}"><br>

			<label for="stop">Stop (pips):</label>
			<input type="text" name="stop" id="stop" value="{{.Stop}}"><br>

			<input type="submit" value="Go">
			<button type="submit" formaction="/exit" formmethod="POST">Exit</button>
		</form>

		{{if .Result}}
		<div class="result">
			<h2>Error:</h2>
			<p><strong>{{.Err}}</strong></p>
		</div>
		{{end}}
	</body>
</html>`

func (svc *Svc) herror(tag string, e error) {
	es := tag
	if e != nil {
		es = fmt.Sprintf("%s: %v", tag, e)
	}
	out := &Out{ Err: es, Result: true }
	svc.tmple.Execute(svc.w, out)
}

func (svc *Svc) formHas(k string) bool {
	v := svc.r.FormValue(k)
	return len(v) > 0
}

func (svc *Svc) formStr(k string) (string, bool) {
	v := svc.r.FormValue(k)
	if len(v) == 0 {
		svc.herror("no "+k, nil)
		return "", false
	}
	return v, true
}

func (svc *Svc) formNum(k string) (float64, bool) {
	v, ok := svc.formStr(k)
	if !ok {
		return 0.0, false
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		svc.herror(k+": bad number", nil)
		return 0.0, false
	}
	return n, true
}

func (svc *Svc) handler(w http.ResponseWriter, r *http.Request) {
	var err error
	svc.ig, err = New()
	if err != nil {
		svc.herror("IG connect error", err)
		return
	}
	svc.w = w
	svc.r = r
	if r.Method != http.MethodPost {
		svc.herror("no input", nil)
		return
	}
	r.ParseForm()
	var ok bool
	if svc.Name, ok = svc.formStr("ticker"); !ok {
		return
	}
	svc.Stop = 0
	if svc.formHas("stop") {
		if svc.Stop, ok = svc.formNum("stop"); !ok {
			return
		}
	}
	if svc.Risk, ok = svc.formNum("risk"); !ok {
		return
	}
	f, err := svc.ig.Get("CS.D."+svc.Name+suff)
	if err != nil {
		svc.herror("IG", err)
		return
	}
	c := f.CurrEurs
	if c == 0.0 {
		c = 1
	}
	crisk := svc.Risk / c
	lots := 0.0
	if svc.Stop != 0 && f.PipVal != 0 {
		lots = svc.Stop * f.PipVal  / crisk
	}
	out := &Out{
		Forex: *f,
		SpreadEur: f.Spread * f.PipVal * f.CurrEurs,
		Stop:	svc.Stop,
		StopCurr: svc.Stop * f.PipVal,
		StopEur : svc.Stop * f.PipVal * c,
		Risk:     svc.Risk,
		RiskCurr:  crisk,
		Lots:   lots,
		Result: true,
	}
	svc.tmpl.Execute(svc.w, out) // display entered input
}

func browse() {
	url := "http://localhost:8080"
	fmt.Println("open ", url)
	switch runtime.GOOS {
	case "linux":
		exec.Command("xdg-open", url).Start()
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	}
}
