package env

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
)

type activityTestTransport struct {
	SSHTransport
	client *sftp.Client
}

func (t activityTestTransport) WithSFTP(_ context.Context, op SFTPOp) (any, error) {
	return op.Run(t.client)
}

func newActivityTestClient(t *testing.T) *sftp.Client {
	t.Helper()
	clientReads, serverWrites := io.Pipe()
	serverReads, clientWrites := io.Pipe()
	fs := activityTestFS{root: t.TempDir()}
	server := sftp.NewRequestServer(pipeRWC{serverReads, serverWrites}, sftp.Handlers{
		FileGet: fs, FilePut: fs, FileCmd: fs, FileList: fs,
	})
	go func() {
		_ = server.Serve()
		_ = serverWrites.Close()
	}()
	client, err := sftp.NewClientPipe(clientReads, clientWrites)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client
}

func TestSFTPMutationsRenewActivity(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"write", "mkdir", "remove", "createtemp"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			client := newActivityTestClient(t)
			require.NoError(t, client.Mkdir("/tmp"))
			transport := activityTestTransport{client: client}
			ctx := context.Background()
			for attempt := 0; attempt < 2; attempt++ {
				require.NoError(t, doSFTPWrite(client, remoteActivityMarker, []byte("old"), 0o600))
				old := time.Unix(100, 0)
				require.NoError(t, client.Chtimes(remoteActivityMarker, old, old))
				start := time.Now().Truncate(time.Second)
				switch operation {
				case "write":
					require.NoError(t, sftpWriteFile(ctx, transport, "/value", []byte("B"), 0o600))
				case "mkdir":
					require.NoError(t, sftpMkdirAll(ctx, transport, "/directory", 0o755))
				case "remove":
					require.NoError(t, doSFTPWrite(client, "/value", []byte("B"), 0o600))
					require.NoError(t, sftpRemove(ctx, transport, "/value"))
				case "createtemp":
					_, err := sftpCreateTemp(ctx, transport, "/", "scratch-*")
					require.NoError(t, err)
				}
				info, err := client.Stat(remoteActivityMarker)
				require.NoError(t, err)
				require.False(t, info.ModTime().Before(start), "mutation did not renew activity")
				require.LessOrEqual(t, info.ModTime().Unix(), time.Now().Unix())
				data, err := doSFTPRead(client, remoteActivityMarker)
				require.NoError(t, err)
				require.Empty(t, data, "renewal must use the server clock, not leave the old marker untouched")
			}
		})
	}
}

// TestSFTPReadsRenewActivityThrottled: an agent that only reads for a while
// must still register as activity, but reads create nothing to lose, so they
// renew the marker at most once per interval per connection.
func TestSFTPReadsRenewActivityThrottled(t *testing.T) {
	t.Parallel()
	client := newActivityTestClient(t)
	require.NoError(t, client.Mkdir("/tmp"))
	require.NoError(t, doSFTPWrite(client, "/value", []byte("B"), 0o600))
	transport := activityTestTransport{client: client}
	ctx := context.Background()

	staleMarker := func() {
		t.Helper()
		require.NoError(t, doSFTPWrite(client, remoteActivityMarker, []byte("old"), 0o600))
		old := time.Unix(100, 0)
		require.NoError(t, client.Chtimes(remoteActivityMarker, old, old))
	}
	markerRenewed := func() bool {
		t.Helper()
		data, err := doSFTPRead(client, remoteActivityMarker)
		require.NoError(t, err)
		return len(data) == 0
	}

	staleMarker()
	_, err := sftpReadFile(ctx, transport, "/value")
	require.NoError(t, err)
	require.True(t, markerRenewed(), "a read must renew a stale marker")

	staleMarker()
	_, err = sftpReadDir(ctx, transport, "/")
	require.NoError(t, err)
	_, err = sftpStat(ctx, transport, "/value")
	require.NoError(t, err)
	require.False(t, markerRenewed(), "reads within the throttle interval must not touch the marker again")

	require.True(t, remoteReadTouches.reserve(client, time.Now().Add(2*remoteReadTouchInterval)),
		"once the interval has elapsed a read must renew the marker again")
}

func TestReadTouchThrottle(t *testing.T) {
	t.Parallel()

	t.Run("concurrent reads reserve exactly one touch per interval", func(t *testing.T) {
		t.Parallel()
		var throttle readTouchThrottle
		client := new(sftp.Client)
		now := time.Now()
		var wg sync.WaitGroup
		var won atomic.Int32
		for i := 0; i < 64; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if throttle.reserve(client, now) {
					won.Add(1)
				}
			}()
		}
		wg.Wait()
		require.Equal(t, int32(1), won.Load())
		require.False(t, throttle.reserve(client, now.Add(remoteReadTouchInterval-time.Millisecond)))
		require.True(t, throttle.reserve(client, now.Add(remoteReadTouchInterval)))
	})

	t.Run("memory of clients is bounded, evicting the oldest", func(t *testing.T) {
		t.Parallel()
		var throttle readTouchThrottle
		now := time.Now()
		first := new(sftp.Client)
		require.True(t, throttle.reserve(first, now))
		for i := 1; i <= remoteReadTouchClients; i++ {
			require.True(t, throttle.reserve(new(sftp.Client), now.Add(time.Duration(i)*time.Millisecond)))
		}
		require.Len(t, throttle.last, remoteReadTouchClients)
		_, retained := throttle.last[first]
		require.False(t, retained, "the oldest reservation must be the one evicted")
	})
}

func TestSFTPMutationOutcomeWithUnavailableMarker(t *testing.T) {
	t.Parallel()
	client := newActivityTestClient(t)
	transport := activityTestTransport{client: client}
	ctx := context.Background()
	// This filesystem deliberately has no /tmp.
	require.NoError(t, sftpWriteFile(ctx, transport, "/value", []byte("B"), 0o600))
	data, err := doSFTPRead(client, "/value")
	require.NoError(t, err)
	require.Equal(t, "B", string(data))
	require.NoError(t, sftpMkdirAll(ctx, transport, "/directory", 0o755))
	require.NoError(t, sftpRemove(ctx, transport, "/value"))
	_, err = client.Stat("/value")
	require.True(t, os.IsNotExist(err))
	require.Error(t, sftpWriteFile(ctx, transport, "/absent/value", []byte("B"), 0o600))
}

type activityTestFS struct {
	root string
}

func (fs activityTestFS) path(r *sftp.Request) string {
	return filepath.Join(fs.root, filepath.Clean("/"+r.Filepath))
}

func (fs activityTestFS) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	return os.Open(fs.path(r))
}

func (fs activityTestFS) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	flags := os.O_WRONLY
	if r.Pflags().Creat {
		flags |= os.O_CREATE
	}
	if r.Pflags().Trunc {
		flags |= os.O_TRUNC
	}
	return os.OpenFile(fs.path(r), flags, 0o600)
}

func (fs activityTestFS) Filecmd(r *sftp.Request) error {
	path := fs.path(r)
	switch r.Method {
	case "Mkdir":
		return os.Mkdir(path, 0o755)
	case "Remove":
		return os.Remove(path)
	case "Setstat":
		flags, attrs := r.AttrFlags(), r.Attributes()
		if flags.Permissions {
			if err := os.Chmod(path, attrs.FileMode()); err != nil {
				return err
			}
		}
		if flags.Acmodtime {
			return os.Chtimes(path, time.Unix(int64(attrs.Atime), 0), time.Unix(int64(attrs.Mtime), 0))
		}
		return nil
	default:
		return os.ErrInvalid
	}
}

type activityTestListing []os.FileInfo

func (l activityTestListing) ListAt(dst []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[offset:])
	if int(offset)+n == len(l) {
		return n, io.EOF
	}
	return n, nil
}

func (fs activityTestFS) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	info, err := os.Stat(fs.path(r))
	if err != nil {
		return nil, err
	}
	return activityTestListing{info}, nil
}
