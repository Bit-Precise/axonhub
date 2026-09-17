package biz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func TestCodexInstallationIDs(t *testing.T) {
	createdAt := time.Unix(1_700_000_000, 123)
	base := &ent.Channel{ID: 42, CreatedAt: createdAt}

	require.Nil(t, codexInstallationIDs(base))

	base.Settings = &objects.ChannelSettings{
		OverrideCodexInstallationID: true,
		CodexInstallationIDCount:    3,
	}
	ids := codexInstallationIDs(base)
	require.Len(t, ids, 3)
	require.Equal(t, ids, codexInstallationIDs(base))
	require.Len(t, map[string]struct{}{ids[0]: {}, ids[1]: {}, ids[2]: {}}, 3)
	other := &ent.Channel{ID: 43, CreatedAt: createdAt, Settings: base.Settings}
	require.NotEqual(t, ids, codexInstallationIDs(other))

	base.Settings.CodexInstallationIDCount = 10
	require.Len(t, codexInstallationIDs(base), objects.MaxCodexInstallationIDCount)
}

func TestValidateCodexInstallationIDSettings(t *testing.T) {
	require.NoError(t, ValidateCodexInstallationIDSettings(nil))
	require.NoError(t, ValidateCodexInstallationIDSettings(&objects.ChannelSettings{
		CodexInstallationIDCount: 99,
	}))
	require.NoError(t, ValidateCodexInstallationIDSettings(&objects.ChannelSettings{
		OverrideCodexInstallationID: true,
		CodexInstallationIDCount:    1,
	}))
	require.Error(t, ValidateCodexInstallationIDSettings(&objects.ChannelSettings{
		OverrideCodexInstallationID: true,
		CodexInstallationIDCount:    0,
	}))
	require.Error(t, ValidateCodexInstallationIDSettings(&objects.ChannelSettings{
		OverrideCodexInstallationID: true,
		CodexInstallationIDCount:    objects.MaxCodexInstallationIDCount + 1,
	}))
}
