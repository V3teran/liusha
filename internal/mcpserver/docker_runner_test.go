package mcpserver_test

import (
	"testing"

	"github.com/V3teran/liusha/internal/mcpserver"
	"github.com/stretchr/testify/require"
)

func TestNewDockerRunner_EmptyContainerErrors(t *testing.T) {
	_, err := mcpserver.NewDockerRunner("")
	require.Error(t, err)
}

func TestNewDockerRunner_OK(t *testing.T) {
	r, err := mcpserver.NewDockerRunner("liusha-vuln-tools")
	require.NoError(t, err)
	require.NotNil(t, r)
}
