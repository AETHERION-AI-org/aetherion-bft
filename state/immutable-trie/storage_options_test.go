package itrie

import (
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"
)

func openPlainForTest(path string) (*KVStorage, error) {
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		return nil, err
	}

	return &KVStorage{db}, nil
}

func TestTrieOpenTables(t *testing.T) {
	t.Parallel()

	require.Equal(t, trieOpenTablesWanted, trieOpenTables(1_048_576))
	require.Equal(t, trieOpenTablesWanted, trieOpenTables(65_535))
	require.Equal(t, 8_192-trieReservedHandles, trieOpenTables(8_192))
	require.Equal(t, trieMinOpenTables, trieOpenTables(1_024))
}

func TestTrieLevelDBOptions_ReadsDataWrittenWithoutFilter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	plain, err := openPlainForTest(dir)
	require.NoError(t, err)
	require.NoError(t, plain.Put([]byte("key"), []byte("value")))
	require.NoError(t, plain.Close())

	tuned, err := NewLevelDBStorage(dir, hclog.NewNullLogger())
	require.NoError(t, err)

	defer tuned.Close()

	got, ok, err := tuned.Get([]byte("key"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []byte("value"), got)
}
