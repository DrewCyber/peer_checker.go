package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/quic-go/quic-go"
)

var (
	PEER_REGEX = regexp.MustCompile(`(tcp|tls|quic)://([a-z0-9\.\-\:\[\]]+):([0-9]+)`)
)

const connTimeout = 5 * time.Second

type Peer struct {
	URI       string        `json:"uri"`
	protocol  string        // unexported, ignored by json
	host      string        // unexported, ignored by json
	port      int           // unexported, ignored by json
	Region    string        `json:"region"`
	Country   string        `json:"country"`
	Up        bool          `json:"up"`
	Latency   time.Duration `json:"-"`       // Internal use, hidden from JSON
	LatencyMs float64       `json:"latency"` // Exported for JSON in milliseconds
}

// JSONResult defines the structure for json output
type JSONResult struct {
	Alive  []Peer `json:"alive"`
	Source []Peer `json:"source"`
}

func getPeers(dataDir string, regions []string, countries []string) ([]Peer, error) {
	peers := []Peer{}
	allRegions, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}

	allCountries := []string{}
	for _, region := range allRegions {
		if region.Name() != ".git" && region.Name() != "other" && region.IsDir() {
			countries, err := os.ReadDir(dataDir + "/" + region.Name())
			if err != nil {
				return nil, err
			}
			for _, country := range countries {
				if strings.HasSuffix(country.Name(), ".md") {
					allCountries = append(allCountries, country.Name())
				}
			}
		}
	}

	if len(regions) == 0 {
		regions = make([]string, len(allRegions))
		for i, region := range allRegions {
			regions[i] = region.Name()
		}
	}
	if len(countries) == 0 {
		countries = allCountries
	}

	for _, region := range regions {
		for _, country := range countries {
			cfile := fmt.Sprintf("%s/%s/%s", dataDir, region, country)
			if _, err := os.Stat(cfile); err == nil {
				content, err := os.ReadFile(cfile)
				if err != nil {
					return nil, err
				}
				matches := PEER_REGEX.FindAllStringSubmatch(string(content), -1)
				for _, match := range matches {
					uri := match[0]
					protocol := match[1]
					host := match[2]
					port, _ := strconv.Atoi(match[3])
					peers = append(peers, Peer{
						URI:      uri,
						protocol: protocol,
						host:     host,
						port:     port,
						Region:   region,
						Country:  country,
					})
				}
			}
		}
	}

	return peers, nil
}

func resolve(name string, resolver func(string) ([]net.IP, error)) (string, error) {
	if strings.HasPrefix(name, "[") {
		return name[1 : len(name)-1], nil
	}

	ips, err := resolver(name)
	if err != nil {
		return "", err
	}
	return ips[0].String(), nil
}

func isUp(peer *Peer) {
	addr, err := resolve(peer.host, net.LookupIP)
	if err != nil {
		slog.Debug("Resolve error:", "msg", err, "type", fmt.Sprintf("%T", err))
		return
	}

	var duration time.Duration

	switch peer.protocol {
	case "tcp", "tls":
		startTime := time.Now()
		// Dial the TCP/TLS server
		conn, err := net.DialTimeout("tcp", "["+addr+"]:"+strconv.Itoa(peer.port), connTimeout)
		if err != nil {
			slog.Debug("Connection error:", "msg", err, "type", fmt.Sprintf("%T", err))
			return
		}
		defer conn.Close()
		duration = time.Since(startTime)
		peer.Up = true
	case "quic":
		// Create a context
		ctx := context.Background()

		// Dial the QUIC server
		startTime := time.Now()
		conn, err := quic.DialAddr(ctx, "["+addr+"]:"+strconv.Itoa(peer.port), &tls.Config{InsecureSkipVerify: true}, nil)
		if err != nil {
			slog.Debug("Connection error:", "msg", err, "type", fmt.Sprintf("%T", err))
			return
		}
		defer conn.CloseWithError(0, "Closing connection")
		duration = time.Since(startTime)
		peer.Up = true
	}

	if peer.Up {
		peer.Latency = duration
		// Convert duration to milliseconds with floating point precision
		peer.LatencyMs = float64(duration.Microseconds()) / 1000.0
	}
}

func printResults(results []Peer) {
	fmt.Println("Report date:", time.Now().Format(time.RFC1123))

	fmt.Println("Dead peers:")
	deadTable := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	fmt.Fprintln(deadTable, "URI\tLocation")
	for _, p := range results {
		if !p.Up {
			fmt.Fprintf(deadTable, "%s\t%s/%s\n", p.URI, p.Region, p.Country)
		}
	}
	deadTable.Flush()

	fmt.Println("\n\nAlive peers (sorted by latency):")
	aliveTable := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	fmt.Fprintln(aliveTable, "URI\tLatency (ms)\tLocation")
	alivePeers := []Peer{}
	for _, p := range results {
		if p.Up {
			alivePeers = append(alivePeers, p)
		}
	}
	sort.Slice(alivePeers, func(i, j int) bool {
		return alivePeers[i].Latency < alivePeers[j].Latency
	})
	for _, p := range alivePeers {
		fmt.Fprintf(aliveTable, "%s\t%.3f\t%s/%s\n", p.URI, p.LatencyMs, p.Region, p.Country)
	}
	aliveTable.Flush()
}

func printJSON(peers []Peer) {
	alivePeers := []Peer{}
	for _, p := range peers {
		if p.Up {
			alivePeers = append(alivePeers, p)
		}
	}

	// Sort alive peers by latency (same logic as text output)
	sort.Slice(alivePeers, func(i, j int) bool {
		return alivePeers[i].Latency < alivePeers[j].Latency
	})

	output := JSONResult{
		Alive:  alivePeers,
		Source: peers,
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
	}
}

func main() {
	// Define flags
	jsonOutput := flag.Bool("json", false, "Output results in JSON format")
	flag.Parse()

	// Handle positional arguments (path is expected after flags)
	args := flag.Args()
	if len(args) != 1 {
		fmt.Printf("Usage: %s [-json] [path to public_peers repository on a disk]\n", os.Args[0])
		fmt.Printf("I.e.:  %s ~/Projects/yggdrasil/public_peers\n", os.Args[0])
		return
	}

	dataDir := args[0]

	peers, err := getPeers(dataDir, nil, nil)
	if err != nil {
		fmt.Printf("Can't find peers in a directory: %s\n", dataDir)
		return
	}

	var wg sync.WaitGroup

	for i := range peers {
		wg.Add(1)
		go func(p *Peer) {
			defer wg.Done()
			isUp(p)
		}(&peers[i])
	}

	wg.Wait()

	if *jsonOutput {
		printJSON(peers)
	} else {
		printResults(peers)
	}
}
