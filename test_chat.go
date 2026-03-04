package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"orchids-api/internal/config"
	"orchids-api/internal/grok"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))

	cfg, _, err := config.Load("./config.json")
	if err != nil {
		fmt.Printf("cfg load err: %v\n", err)
		return
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB})
	ctx := context.Background()
	keys, err := rdb.Keys(ctx, cfg.RedisPrefix+"accounts:id:124").Result()
	if err != nil || len(keys) == 0 {
		fmt.Printf("no keys found: %v\n", err)
		return
	}

	raw, _ := rdb.Get(ctx, keys[0]).Result()
	var acc map[string]interface{}
	json.Unmarshal([]byte(raw), &acc)
	token, _ := acc["token"].(string)

	client := grok.New(cfg)

	fmt.Printf("\n--- Testing VerifyToken (doChat fallback) ---\n")

	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	info, err := client.VerifyToken(reqCtx, token, "grok-3")
	if err != nil {
		fmt.Printf("VerifyToken failed: %v\n", err)
	} else {
		fmt.Printf("VerifyToken SUCCESS: %+v\n", info)
	}
}
