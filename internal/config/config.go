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

// EnvOverridden reports whether key's env var (see envName) is currently
// set. Env always wins over a stored setting (String/Int/Bool above), so
// a caller building a "this field is locked" UI hint (Task 10's
// /api/settings) can ask directly rather than re-deriving envName
// itself.
func (c *Config) EnvOverridden(key string) bool {
	return c.getenv(envName(key)) != ""
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
