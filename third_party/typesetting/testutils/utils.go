// SPDX-License-Identifier: Unlicense OR BSD-3-Clause

package testutils

import (
	"testing"
)

func Assert(t testing.TB, b bool) {
	t.Helper()
	AssertC(t, b, "assertion error")
}

func AssertNoErr(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func AssertC(t testing.TB, b bool, context string) {
	t.Helper()
	if !b {
		t.Fatal(context)
	}
}
