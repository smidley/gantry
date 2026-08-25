// Package config resolves runtime configuration with the precedence
// env (GANTRY_*) > settings table > compiled default.
package config

import (
	"strconv"
	"strings"

	"github.com/smidley/gantry/internal/store"
)

type Config struct {
	st     *store.Store
	getenv func(string) string
}

func New(st *store.Store, getenv func(string) string) *Config {
	return &Config{st: st, getenv: getenv}
}

func envName(key string) string {
	return "GANTRY_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func (c *Config) String(key, def string) string {
	if v := c.getenv(envName(key)); v != "" {
		return v
	}
	if v, ok, err := c.st.SettingGet(key); err == nil && ok {
		return v
	}
	return def
}

func (c *Config) Int(key string, def int) int {
	if n, err := strconv.Atoi(c.String(key, strconv.Itoa(def))); err == nil {
		return n
	}
	return def
}

func (c *Config) Bool(key string, def bool) bool {
	v := strings.ToLower(c.String(key, ""))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
