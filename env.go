package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Env map[string]string

func (e Env) Pairs() []string {
	pairs := []string{}

	for k, v := range e {
		pairs = append(pairs, k+"="+v)
	}

	return pairs
}

func envFromPairs(pairs []string) Env {
	env := make(Env)

	for _, pair := range pairs {
		split := strings.SplitN(pair, "=", 2)
		key := split[0]
		value := ""
		if len(split) == 2 {
			value = split[1]
		}

		env[key] = value
	}

	return env
}

func loadEnv(startEnv Env, envPath ...string) (Env, error) {
	loadedEnv, err := godotenv.Read(envPath...)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to read env files: %w", err)
	}

	env := make(Env)
	maps.Copy(env, startEnv)
	maps.Copy(env, loadedEnv)

	return env, nil
}
