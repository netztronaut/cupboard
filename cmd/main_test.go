package main

import (
	"flag"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestSetupCacheNamespaces(t *testing.T) {
	cacheOpts := setupCacheNamespaces("default")
	assert.Len(t, cacheOpts.DefaultNamespaces, 1)
	assert.Contains(t, cacheOpts.DefaultNamespaces, "default")

	cacheOpts = setupCacheNamespaces("ns1,ns2,ns3")
	assert.Len(t, cacheOpts.DefaultNamespaces, 3)
	assert.Contains(t, cacheOpts.DefaultNamespaces, "ns1")
	assert.Contains(t, cacheOpts.DefaultNamespaces, "ns2")
	assert.Contains(t, cacheOpts.DefaultNamespaces, "ns3")

	cacheOpts = setupCacheNamespaces(" ns1 , ns2 ")
	assert.Len(t, cacheOpts.DefaultNamespaces, 2)
	assert.Contains(t, cacheOpts.DefaultNamespaces, "ns1")
	assert.Contains(t, cacheOpts.DefaultNamespaces, "ns2")

	cacheOpts = setupCacheNamespaces("")
	count := 0
	for ns := range cacheOpts.DefaultNamespaces {
		if strings.TrimSpace(ns) != "" {
			count++
		}
	}
	assert.Equal(t, 0, count)
}

func TestFirstNonEmpty(t *testing.T) {
	result := firstNonEmpty("value1", "value2", "value3")
	assert.Equal(t, "value1", result)

	result = firstNonEmpty("", "value2", "value3")
	assert.Equal(t, "value2", result)

	result = firstNonEmpty("value1", "", "value3")
	assert.Equal(t, "value1", result)

	result = firstNonEmpty("", "", "")
	assert.Equal(t, "", result)

	result = firstNonEmpty("single")
	assert.Equal(t, "single", result)

	result = firstNonEmpty("")
	assert.Equal(t, "", result)
}

func TestResolveStringFlag(t *testing.T) {
	setFlags := map[string]bool{
		"test-flag": true,
	}

	result := resolveStringFlag(nil, setFlags, "test-flag", "test-config-key", "current-value")
	assert.Equal(t, "current-value", result)

	config := viper.New()
	config.Set("test-config-key", "config-value")

	setFlags = map[string]bool{}
	result = resolveStringFlag(config, setFlags, "test-flag", "test-config-key", "current-value")
	assert.Equal(t, "config-value", result)

	result = resolveStringFlag(config, setFlags, "test-flag", "nonexistent-key", "current-value")
	assert.Equal(t, "current-value", result)
}

func TestResolveBoolFlag(t *testing.T) {
	setFlags := map[string]bool{
		"test-flag": true,
	}

	result := resolveBoolFlag(nil, setFlags, "test-flag", "test-config-key", true)
	assert.Equal(t, true, result)

	config := viper.New()
	config.Set("test-config-key", false)

	setFlags = map[string]bool{}
	result = resolveBoolFlag(config, setFlags, "test-flag", "test-config-key", true)
	assert.Equal(t, false, result)

	result = resolveBoolFlag(config, setFlags, "test-flag", "nonexistent-key", true)
	assert.Equal(t, true, result)
}

func TestExplicitFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	setFlags := explicitFlags(fs)
	assert.NotNil(t, setFlags)
	assert.Empty(t, setFlags)
}
