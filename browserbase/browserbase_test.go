package browserbase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-browserbase/browserbase"
	"github.com/circleci-functions/setup-browserbase/internal/testing/fakebrowserbase"
)

func TestCreate_TagsAndBoundsTheSession(t *testing.T) {
	srv := fakebrowserbase.New(t)
	c := browserbase.New(srv.URL, fakebrowserbase.APIKey)

	s, err := c.Create(context.Background(), browserbase.CreateOptions{
		Timeout:  30 * time.Minute,
		Metadata: map[string]any{"ci": map[string]any{"run": "r1"}},
	})
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(s.ConnectURL, srv.ConnectURL))
	got := srv.Sessions()
	assert.Assert(t, cmp.Len(got, 1))
	assert.Check(t, cmp.Equal(got[0].Timeout, 1800))
	assert.Check(t, cmp.DeepEqual(got[0].Metadata, map[string]any{"ci": map[string]any{"run": "r1"}}))
}

func TestCreate_WaitsOutAPlanLimit(t *testing.T) {
	srv := fakebrowserbase.New(t)
	srv.LimitNext(1)
	c := browserbase.New(srv.URL, fakebrowserbase.APIKey)

	_, err := c.Create(context.Background(), browserbase.CreateOptions{})
	assert.NilError(t, err)
	assert.Check(t, cmp.Len(srv.Sessions(), 1))
}

func TestCreate_NamesThePlanLimitWhenItDoesNotClear(t *testing.T) {
	srv := fakebrowserbase.New(t)
	srv.LimitNext(100)
	c := browserbase.New(srv.URL, fakebrowserbase.APIKey)
	c.Wait = 1500 * time.Millisecond

	_, err := c.Create(context.Background(), browserbase.CreateOptions{})
	var limit *browserbase.LimitError
	assert.Assert(t, errors.As(err, &limit), "got %v", err)
	assert.Check(t, cmp.ErrorContains(err, "Free: 3 concurrent, 5 a minute"))
}

func TestListAndRelease_OnlyThisRunsSessions(t *testing.T) {
	srv := fakebrowserbase.New(t)
	c := browserbase.New(srv.URL, fakebrowserbase.APIKey)
	ctx := context.Background()
	for _, run := range []string{"mine", "theirs", "mine"} {
		_, err := c.Create(ctx, browserbase.CreateOptions{Metadata: map[string]any{"ci": map[string]any{"run": run}}})
		assert.NilError(t, err)
	}

	mine, err := c.List(ctx, "user_metadata['ci']['run']:'mine'", "RUNNING")
	assert.NilError(t, err)
	assert.Assert(t, cmp.Len(mine, 2))
	for _, s := range mine {
		assert.NilError(t, c.Release(ctx, s.ID, ""))
	}
	still, err := c.List(ctx, "", "RUNNING")
	assert.NilError(t, err)
	assert.Assert(t, cmp.Len(still, 1))
	assert.Check(t, cmp.Equal(still[0].ID, "sess_2"))

	// Releasing a session that is gone is not an error.
	assert.NilError(t, c.Release(ctx, "sess_missing", ""))
}

func TestClient_RejectedKey(t *testing.T) {
	srv := fakebrowserbase.New(t)
	_, err := browserbase.New(srv.URL, "wrong").Create(context.Background(), browserbase.CreateOptions{})
	assert.Check(t, cmp.ErrorContains(err, "Unauthorized (401)"))
}

// Browserbase refuses an empty metadata value, which an unset variable gives.
func TestCreate_EmptyMetadataValueIsRefused(t *testing.T) {
	srv := fakebrowserbase.New(t)
	_, err := browserbase.New(srv.URL, fakebrowserbase.APIKey).Create(context.Background(), browserbase.CreateOptions{
		Metadata: map[string]any{"ci": map[string]any{"job": ""}},
	})
	assert.Check(t, cmp.ErrorContains(err, "not a valid metadata value"))
}
