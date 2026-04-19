package mcpserver_test

import (
	"testing"

	"github.com/V3teran/liusha/internal/mcpserver"
	"github.com/V3teran/liusha/internal/scope"
	"github.com/stretchr/testify/require"
)

func TestNewServer_NilPolicyErrors(t *testing.T) {
	_, err := mcpserver.New(nil)
	require.Error(t, err)
}

func TestNewServer_OK(t *testing.T) {
	p, err := scope.NewPolicy(scope.Rules{
		Domains: []string{"example.com"},
	})
	require.NoError(t, err)
	s, err := mcpserver.New(p)
	require.NoError(t, err)
	require.NotNil(t, s)
}
