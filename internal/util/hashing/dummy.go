package hashing

import (
	"sync"

	"golang.org/x/crypto/argon2"
)

// dummySalt is a fixed, non-secret salt. Its only job is to make the dummy
// comparison below cost the same as a real one; it never protects anything.
var dummySalt = []byte("cloudtecko-dummy")

var dummyParamsOnce sync.Once
var dummyParams *Argon2Params

// CompareDummyPasswordAndHash performs the Argon2 work of a real password check
// and discards the result.
//
// Login must not answer faster for an unknown username than for a known one.
// Without this, a caller can tell whether an account exists purely from response
// time: a real user costs a full Argon2id derivation (~64MB, tens of
// milliseconds by default) while an unknown one used to return in microseconds
// straight after the failed database lookup. That is a reliable, remotely
// observable account-enumeration oracle, and it needs no credentials at all.
//
// The parameters deliberately come from the same source the real path uses, so
// the two stay in step if the cost factors are ever retuned.
func CompareDummyPasswordAndHash(password string) {
	dummyParamsOnce.Do(func() {
		dummyParams = DefaultArgon2ParamsFromEnv()
	})

	key := argon2.IDKey(
		[]byte(password),
		dummySalt,
		dummyParams.Time,
		dummyParams.Memory,
		dummyParams.Threads,
		dummyParams.KeyLength,
	)

	// Keep the compiler from optimising the derivation away.
	runtimeSink(key)
}

// runtimeSink exists purely so the derived key is observably used.
//
//go:noinline
func runtimeSink(key []byte) {
	if len(key) == 0 {
		panic("argon2 produced an empty key")
	}
}
