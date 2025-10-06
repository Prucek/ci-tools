package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

func TestOptionsValidate(t *testing.T) {
	good := options{
		Options:   config.Options{LogLevel: "info"},
		configDir: "path/to/dir",
		tokenPath: "path/to/token",
		targetOrg: "org",
		gitName:   "openshift-bot",
		gitEmail:  "opensthift-bot@redhat.com",
	}
	testcases := []struct {
		description string
		bad         *options

		org  string
		repo string

		expectedErrors int
	}{
		{
			description: "good options pass validation",
		},
		{
			description:    "missing --config-dir does not pass validation",
			bad:            &options{tokenPath: "path/to/token", targetOrg: "org"},
			expectedErrors: 3,
		},
		{
			description:    "missing --token-path does not pass validation",
			bad:            &options{configDir: "path/to/dir", targetOrg: "org", Options: config.Options{LogLevel: "info"}},
			expectedErrors: 3,
		},
		{
			description:    "missing --target-org does not pass validation",
			bad:            &options{configDir: "path/to/dir", tokenPath: "path/to/token", Options: config.Options{LogLevel: "info"}},
			expectedErrors: 3,
		},
		{
			description: "--only-org different from --target-org passes validation",
			org:         "different-org",
		},
		{
			description:    "--only-org same as --target-org does not pass validation",
			org:            "org",
			expectedErrors: 1,
		},
		{
			description:    "--only-org and --only-repo does not pass validation",
			org:            "different-org",
			repo:           "another-org/repo",
			expectedErrors: 1,
		},
		{
			description:    "bad --only-repo does not pass validation",
			repo:           "not-a-repo",
			expectedErrors: 1,
		},
		{
			description:    "--only-repo in --target-org does not pass validation",
			repo:           "org/repo",
			expectedErrors: 1,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			opts := good
			if tc.bad != nil {
				opts = *tc.bad
			}
			opts.org = tc.org
			opts.repo = tc.repo

			errs := opts.validate()
			if len(errs) != tc.expectedErrors {
				t.Errorf("%s: expected %d errors, got %d (%v)", tc.description, tc.expectedErrors, len(errs), errs)
			}
		})
	}
}

func TestOptionsMakeFilter(t *testing.T) {
	official := &api.ReleaseBuildConfiguration{
		PromotionConfiguration: &api.PromotionConfiguration{
			Targets: []api.PromotionTarget{{
				Namespace: "ocp",
			}},
		},
	}
	notOfficial := &api.ReleaseBuildConfiguration{
		PromotionConfiguration: &api.PromotionConfiguration{
			Targets: []api.PromotionTarget{{
				Namespace: "not-ocp",
			}},
		},
	}
	// Check that our assumptions about what is an official image still holds
	if !api.BuildsAnyOfficialImages(official, api.WithoutOKD) {
		t.Fatal("Test data assumed to be official images are not official images")
	}
	if api.BuildsAnyOfficialImages(notOfficial, api.WithoutOKD) {
		t.Fatal("Test data assumed to be non-official images are official images")
	}
	testcases := []struct {
		description   string
		optionOrg     string
		optionRepo    string
		repoOrg       string
		repoName      string
		callbackError error
		notOfficial   bool

		expectCall  bool
		expectError bool
	}{
		{
			description: "no org option passed, callbacks are not filtered",
			expectCall:  true,
		},
		{
			description: "org option passed, callback is made for repo in that org",
			optionOrg:   "org",
			repoOrg:     "org",
			expectCall:  true,
		},
		{
			description: "org option passed, callback is not made for repo not in that org",
			optionOrg:   "org",
			repoOrg:     "not-org",
			expectCall:  false,
		},
		{
			description: "repo option passed, callback is made for that repo",
			optionRepo:  "org/repo",
			repoOrg:     "org",
			repoName:    "repo",
			expectCall:  true,
		},
		{
			description: "repo option passed, callback is not made for other repo",
			optionRepo:  "org/repo",
			repoOrg:     "org",
			repoName:    "not-repo",
			expectCall:  false,
		},
		{
			description:   "callback is made and its error is propagated",
			callbackError: fmt.Errorf("FAIL"),
			expectCall:    true,
			expectError:   true,
		},
		{
			description: "no filter options but repo does not build official images, callback is not made",
			notOfficial: true,
			expectCall:  false,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.description, func(t *testing.T) {
			o := &options{
				org:  tc.optionOrg,
				repo: tc.optionRepo,
			}
			ciop := official
			if tc.notOfficial {
				ciop = notOfficial
			}
			info := &config.Info{
				Metadata: api.Metadata{
					Org:  tc.repoOrg,
					Repo: tc.repoName,
				},
			}
			var called bool
			callback := func(*api.ReleaseBuildConfiguration, *config.Info) error {
				called = true
				return tc.callbackError
			}
			err := o.makeFilter(callback)(ciop, info)
			if err == nil && tc.expectError {
				t.Errorf("%s: expected error, got none", tc.description)
			}
			if err != nil && !tc.expectError {
				t.Errorf("%s: got unexpected error: %v", tc.description, err)
			}
			if called != tc.expectCall {
				var expected, actual string
				if !tc.expectCall {
					expected = "not "
				}
				if !called {
					actual = " not"
				}
				t.Errorf("%s: expected callback to %sbe called, it was%s", tc.description, expected, actual)
			}
		})
	}
}

type mockGitCall struct {
	call     string
	output   string
	exitCode int
}

type mockGit struct {
	next     int
	expected []mockGitCall

	t *testing.T
}

func (m *mockGit) exec(_ *logrus.Entry, _ string, command ...string) (string, int, error) {
	cmd := strings.Join(command, " ")
	if m.next >= len(m.expected) {
		m.t.Fatalf("unexpected git call: %s", cmd)
		return "", 0, nil
	}
	if m.expected[m.next].call != cmd {
		m.t.Fatalf("unexpected git call:\n  expected: %s\n  called:   %s", m.expected[m.next].call, cmd)
		return "", 0, nil
	}

	out := m.expected[m.next].output
	exitCode := m.expected[m.next].exitCode
	m.next++

	return out, exitCode, nil
}

func (m mockGit) check() error {
	if m.next != len(m.expected) {
		return fmt.Errorf("unexpected number of git calls: expected %d, done %d", len(m.expected), m.next)
	}
	return nil
}

func TestMirror(t *testing.T) {
	second = time.Millisecond
	token := "TOKEN"
	org, repo, branch := "org", "repo", "branch"
	destOrg := "dest"
	testCases := []struct {
		description string

		src                  location
		dst                  location
		failOnNonexistentDst bool
		confirm              bool

		expectedGitCalls []mockGitCall
		expectError      bool
	}{
		{
			description: "cold cache, confirm, success -> no error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			confirm:     true,
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "dest-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
				{call: "fetch --tags org-repo branch --depth=2"},
				{call: "push --tags https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "cold cache, fails to set up remote -> error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "dest-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo", exitCode: 1},
				{call: "remote add org-repo https://TOKEN@github.com/org/repo", exitCode: 1},
			},
			expectError: true,
		},
		{
			description: "warm cache, confirm, success -> no error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			confirm:     true,
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "dest-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
				{call: "fetch --tags org-repo branch --depth=2"},
				{call: "push --tags https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "warm cache, no confirm, success -> push with dry run",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "dest-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
				{call: "fetch --tags org-repo branch --depth=2"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "warm cache, no confirm, source has more branches -> push with dry run",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "dest-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch\nanother-sha refs/heads/another-branch"},
				{call: "fetch --tags org-repo branch --depth=2"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "warm cache, fails to fetch -> error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "dest-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
				{call: "fetch --tags org-repo branch --depth=2", exitCode: 1},
			},
			expectError: true,
		},
		{
			description: "warm cache, no confirm, fails to push -> error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "dest-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
				{call: "fetch --tags org-repo branch --depth=2"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch", exitCode: 1},
			},
			expectError: true,
		},
		{
			description: "warm cache, branches are in sync -> no fetch, no push",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "source-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
			},
		},
		{
			description: "warm cache, ls-remote source fails with retries -> error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "source-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", exitCode: 1},
				{call: "ls-remote --heads org-repo", exitCode: 1},
				{call: "ls-remote --heads org-repo", exitCode: 1},
				{call: "ls-remote --heads org-repo", exitCode: 1},
				{call: "ls-remote --heads org-repo", exitCode: 1},
			},
			expectError: true,
		},
		{
			description: "warm cache, ls-remote source succeeds after retries -> success",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "source-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", exitCode: 1},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
			},
		},
		{
			description: "warm cache, source branch does not exist -> error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "source-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "some-sha refs/heads/not-the-branch"},
			},
			expectError: true,
		},
		{
			// If git ls-remote fails, destination repository does not exist
			// This is not an error unless failOnNonexistentDst is set
			description: "warm cache, ls-remote destination fails on git -> no error when configured",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", exitCode: 1},
			},
		},
		{
			// If git ls-remote fails, destination repository does not exist
			// This is an error when failOnNonexistentDst is set
			description: "warm cache, ls-remote destination fails on git -> error when configured",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", exitCode: 1},
			},
			failOnNonexistentDst: true,
			expectError:          true,
		},
		{
			description: "warm cache, destination is empty repo, needs many commits -> full fetch then success",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
				{call: "fetch --tags org-repo branch"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "warm cache, destination needs 50 commits -> retries deepening fetches, then success",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "dest-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
				{call: "fetch --tags org-repo branch --depth=2"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=4"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=8"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=16"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=32"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=64"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch"},
			},
		},
		{
			description: "warm cache, destination needs to merge with source -> retries exceeded, then perform merge after fetching --unshallow",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "dest-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
				{call: "fetch --tags org-repo branch --depth=2"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=4"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=8"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=16"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=32"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=64"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --unshallow"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "fetch https://TOKEN@github.com/dest/repo branch"},
				{call: "checkout FETCH_HEAD"},
				{call: "-c user.name=openshift-bot -c user.email=openshift-bot@redhat.com merge org-repo/branch -m DPTP reconciliation from upstream"},
				{call: "push --tags --dry-run https://TOKEN@github.com/dest/repo HEAD:branch"},
			},
		},
		{
			description: "warm cache, destination needs to merge with source -> retries exceeded, then perform merge after fetching --unshallow, merge fails and performs merge --abort",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo", output: "dest-sha refs/heads/branch"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
				{call: "fetch --tags org-repo branch --depth=2"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=4"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=8"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=16"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=32"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --depth=64"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "rev-parse --is-shallow-repository", output: "true"},
				{call: "fetch --tags org-repo branch --unshallow"},
				{
					call:     "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					exitCode: 1,
					output:   "...Updates were rejected because the remote contains work that you do...",
				},
				{call: "fetch https://TOKEN@github.com/dest/repo branch"},
				{call: "checkout FETCH_HEAD"},
				{
					call:     "-c user.name=openshift-bot -c user.email=openshift-bot@redhat.com merge org-repo/branch -m DPTP reconciliation from upstream",
					exitCode: 1,
				},
				{call: "merge --abort"},
			},
		},
		{
			description: "conflicting histories after a force-push result in an error",
			src:         location{org: org, repo: repo, branch: branch},
			dst:         location{org: destOrg, repo: repo, branch: branch},
			expectedGitCalls: []mockGitCall{
				{call: "ls-remote --heads https://TOKEN@github.com/dest/repo"},
				{call: "init"},
				{call: "remote get-url org-repo"},
				{call: "ls-remote --heads org-repo", output: "source-sha refs/heads/branch"},
				{call: "fetch --tags org-repo branch"},
				{
					call: "push --tags --dry-run https://TOKEN@github.com/dest/repo FETCH_HEAD:refs/heads/branch",
					output: `To https://TOKEN@github.com/dest/repo
 ! [rejected]        branch -> branch (non-fast-forward)
error: failed to push some refs to 'https://TOKEN@github.com/dest/repo'
hint: Updates were rejected because the tip of your current branch is behind
hint: its remote counterpart. Integrate the remote changes (e.g.
hint: 'git pull ...') before pushing again.
hint: See the 'Note about fast-forwards' in 'git push --help' for details.
`,
					exitCode: 1,
				},
			},
			expectError: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			git := mockGit{
				expected: tc.expectedGitCalls,
				t:        t,
			}
			m := gitSyncer{
				logger:               logrus.WithField("test", tc.description),
				prefix:               defaultPrefix,
				token:                token,
				confirm:              tc.confirm,
				root:                 "git-dir",
				git:                  git.exec,
				gitName:              "openshift-bot",
				gitEmail:             "openshift-bot@redhat.com",
				failOnNonexistentDst: tc.failOnNonexistentDst,
			}
			err := m.mirror("repo-dir", tc.src, tc.dst)
			if err == nil && tc.expectError {
				t.Error("expected error, got nil")
			}
			if err != nil && !tc.expectError {
				t.Errorf("unexpected error: %v", err)
			}
			if err = git.check(); err != nil {
				t.Errorf("bad git operation: %v", err)
			}
		})
	}
}

func TestDestinationNaming(t *testing.T) {
	testCases := []struct {
		name         string
		sourceOrg    string
		sourceRepo   string
		targetOrg    string
		onlyOrg      string
		flattenOrgs  []string
		expectedRepo string
	}{
		{
			name:         "repo from only-org keeps original name",
			sourceOrg:    "openshift",
			sourceRepo:   "api",
			targetOrg:    "openshift-priv",
			onlyOrg:      "openshift",
			flattenOrgs:  nil,
			expectedRepo: "api",
		},
		{
			name:         "repo from different org gets prefixed name",
			sourceOrg:    "migtools",
			sourceRepo:   "must-gather",
			targetOrg:    "openshift-priv",
			onlyOrg:      "openshift",
			flattenOrgs:  nil,
			expectedRepo: "migtools-must-gather",
		},
		{
			name:         "no only-org specified, non-default orgs get prefixed name",
			sourceOrg:    "migtools",
			sourceRepo:   "must-gather",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  nil,
			expectedRepo: "migtools-must-gather",
		},
		{
			name:         "repo from flatten-org keeps original name",
			sourceOrg:    "openshift-eng",
			sourceRepo:   "ocp-build-data",
			targetOrg:    "openshift-priv",
			onlyOrg:      "openshift",
			flattenOrgs:  []string{"openshift-eng", "migtools"},
			expectedRepo: "ocp-build-data",
		},
		{
			name:         "repo from flatten-org without only-org keeps original name",
			sourceOrg:    "openshift-eng",
			sourceRepo:   "ocp-build-data",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  []string{"openshift-eng"},
			expectedRepo: "ocp-build-data",
		},
		{
			name:         "repo not in flatten-org list gets prefixed",
			sourceOrg:    "custom-org",
			sourceRepo:   "custom-repo",
			targetOrg:    "openshift-priv",
			onlyOrg:      "openshift",
			flattenOrgs:  []string{"another-org"},
			expectedRepo: "custom-org-custom-repo",
		},
		{
			name:         "default flattened orgs keep original names without --only-org",
			sourceOrg:    "openshift",
			sourceRepo:   "installer",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  nil,
			expectedRepo: "installer",
		},
		{
			name:         "default flattened org openshift-eng keeps original name",
			sourceOrg:    "openshift-eng",
			sourceRepo:   "ocp-build-data",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  nil,
			expectedRepo: "ocp-build-data",
		},
		{
			name:         "default flattened org redhat-cne keeps original name",
			sourceOrg:    "redhat-cne",
			sourceRepo:   "cloud-event-proxy",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  nil,
			expectedRepo: "cloud-event-proxy",
		},
		{
			name:         "default flattened org ViaQ keeps original name",
			sourceOrg:    "ViaQ",
			sourceRepo:   "logging-fluentd",
			targetOrg:    "openshift-priv",
			onlyOrg:      "",
			flattenOrgs:  nil,
			expectedRepo: "logging-fluentd",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			o := &options{
				targetOrg:   tc.targetOrg,
				org:         tc.onlyOrg,
				flattenOrgs: tc.flattenOrgs,
			}

			source := location{
				org:    tc.sourceOrg,
				repo:   tc.sourceRepo,
				branch: "main",
			}

			destination := source
			destination.org = o.targetOrg

			// Apply the same logic as in main()
			// Start with the default flattened orgs for backwards compatibility
			flattenedOrgs := sets.New[string](defaultFlattenOrgs...)
			// Add any additional orgs specified via --flatten-org
			flattenedOrgs.Insert(o.flattenOrgs...)
			// The --only-org is also flattened if specified
			if o.org != "" {
				flattenedOrgs.Insert(o.org)
			}
			if !flattenedOrgs.Has(source.org) {
				destination.repo = fmt.Sprintf("%s-%s", source.org, source.repo)
			}

			if destination.repo != tc.expectedRepo {
				t.Errorf("expected destination repo %q, got %q", tc.expectedRepo, destination.repo)
			}
		})
	}
}
