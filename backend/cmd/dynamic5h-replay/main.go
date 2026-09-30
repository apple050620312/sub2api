package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func main() {
	input := flag.String("input", "", "JSON trace file")
	flag.Parse()
	if err := run(*input); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(input string) error {
	if input == "" {
		return fmt.Errorf("-input is required")
	}
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	var trace service.Dynamic5hReplayTrace
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&trace); err != nil {
		return err
	}
	server, err := miniredis.Run()
	if err != nil {
		return err
	}
	defer server.Close()
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()
	report, err := service.ReplayDynamic5hTraffic(context.Background(), repository.NewDynamic5hPressureCache(client), trace, server.FastForward)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
