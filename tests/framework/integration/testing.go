// Copyright 2021 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration

import (
	"os"
	"testing"

	grpclogsettable "github.com/grpc-ecosystem/go-grpc-middleware/logging/settable"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zapgrpc"
	"go.uber.org/zap/zaptest"

	"go.etcd.io/etcd/client/pkg/v3/testutil"
	"go.etcd.io/etcd/client/pkg/v3/verify"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	gofail "go.etcd.io/gofail/runtime"
)

var (
	grpcLogger        grpclogsettable.SettableLoggerV2
	insideTestContext bool
)

func init() {
	grpcLogger = grpclogsettable.ReplaceGrpcLoggerV2()
}

type testOptions struct {
	goLeakDetection bool
	skipInShort     bool
	failpoint       *failpoint
}

type failpoint struct {
	name    string
	payload string
}

func newTestOptions(opts ...TestOption) *testOptions {
	o := &testOptions{goLeakDetection: true, skipInShort: true}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

type TestOption func(opt *testOptions)

// WithoutGoLeakDetection disables checking whether a testcase leaked a goroutine.
func WithoutGoLeakDetection() TestOption {
	return func(opt *testOptions) { opt.goLeakDetection = false }
}

func WithoutSkipInShort() TestOption {
	return func(opt *testOptions) { opt.skipInShort = false }
}

// WithFailpoint registers a go fail point
func WithFailpoint(name, payload string) TestOption {
	return func(opt *testOptions) { opt.failpoint = &failpoint{name: name, payload: payload} }
}

// BeforeTestExternal initializes test context and is targeted for external APIs.
// In general the `integration` package is not targeted to be used outside of
// etcd project, but till the dedicated package is developed, this is
// the best entry point so far (without backward compatibility promise).
func BeforeTestExternal(t testutil.TB) {
	BeforeTest(t, WithoutSkipInShort(), WithoutGoLeakDetection())
}

func BeforeTest(t testutil.TB, opts ...TestOption) {
	t.Helper()
	options := newTestOptions(opts...)

	if insideTestContext {
		t.Fatal("already in test context. BeforeTest was likely already called")
	}

	if options.skipInShort {
		testutil.SkipTestIfShortMode(t, "Cannot create clusters in --short tests")
	}

	if options.goLeakDetection {
		testutil.RegisterLeakDetection(t)
	}

	if options.failpoint != nil && len(options.failpoint.name) != 0 {
		if len(gofail.List()) == 0 {
			t.Skip("please run 'make gofail-enable' before running the test")
		}
		require.NoError(t, gofail.Enable(options.failpoint.name, options.failpoint.payload))
		t.Cleanup(func() {
			require.NoError(t, gofail.Disable(options.failpoint.name))
		})
	}

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	previousInsideTestContext := insideTestContext

	// Integration tests should verify written state as much as possible.
	revertFunc := verify.EnableAllVerifications()

	// Registering cleanup early, such it will get executed even if the helper fails.
	t.Cleanup(func() {
		grpcLogger.Reset()
		insideTestContext = previousInsideTestContext
		os.Chdir(previousWD)
		revertFunc()
	})

	grpcLogger.Set(zapgrpc.NewLogger(zaptest.NewLogger(t).Named("grpc")))
	insideTestContext = true

	os.Chdir(t.TempDir())
}

func assertInTestContext(t testutil.TB) {
	if !insideTestContext {
		t.Errorf("the function can be called only in the test context. Was integration.BeforeTest() called ?")
	}
}

func NewEmbedConfig(tb testing.TB, name string) *embed.Config {
	cfg := embed.NewConfig()
	cfg.Name = name
	lg := zaptest.NewLogger(tb, zaptest.Level(zapcore.InfoLevel)).Named(cfg.Name)
	cfg.ZapLoggerBuilder = embed.NewZapLoggerBuilder(lg)
	cfg.Dir = tb.TempDir()
	return cfg
}

func NewClient(tb testing.TB, cfg clientv3.Config) (*clientv3.Client, error) {
	if cfg.Logger == nil {
		cfg.Logger = zaptest.NewLogger(tb).Named("client")
	}
	return clientv3.New(cfg)
}
