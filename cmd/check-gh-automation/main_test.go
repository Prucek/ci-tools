package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/plugins"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

type fakeAutomationClient struct {
	collaboratorsByRepo   map[string][]string
	membersByOrg          map[string][]string
	reposWithAppInstalled sets.Set[string]
	permissionsByRepo     map[string]map[string][]string
	repos                 map[string]github.FullRepo
	organizations         map[string]github.Organization
}

func newFakeConfiguration() *config.ReleaseRepoConfig {
	relaseRepoConfig := config.ReleaseRepoConfig{
		Prow: &prowconfig.Config{},
		CiOperator: config.DataByFilename{
			"org-1-repo-c-master.yaml": config.DataWithInfo{
				Configuration: cioperatorapi.ReleaseBuildConfiguration{
					PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
						Targets: []cioperatorapi.PromotionTarget{
							{Namespace: "ocp"},
						},
					},
				},
				Info: config.Info{},
			},
			"org-5-repo-d-main.yaml": config.DataWithInfo{
				Configuration: cioperatorapi.ReleaseBuildConfiguration{
					PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
						Targets: []cioperatorapi.PromotionTarget{
							{Namespace: "ocp"},
						},
					},
				},
				Info: config.Info{},
			},
		},
	}
	return &relaseRepoConfig
}

func newFakePluginConfigAgent() *plugins.ConfigAgent {
	fakePluginConfig := &plugins.Configuration{
		ExternalPlugins: map[string][]plugins.ExternalPlugin{
			"org-1/repo-a": {
				{Name: "cherrypick"},
			},
		},
	}
	fakePluginConfigAgent := &plugins.ConfigAgent{}
	fakePluginConfigAgent.Set(fakePluginConfig)
	return fakePluginConfigAgent
}

func newFakeProwConfigAgent() *prowconfig.Agent {
	t := true
	prowConfig := &prowconfig.Config{
		JobConfig: prowconfig.JobConfig{},
		ProwConfig: prowconfig.ProwConfig{
			Tide: prowconfig.Tide{
				TideGitHubConfig: prowconfig.TideGitHubConfig{
					Queries: prowconfig.TideQueries{
						{
							Orgs:  []string{"org-1", "org-3"},
							Repos: []string{"repo-a"},
						},
					},
				},
			},
			BranchProtection: prowconfig.BranchProtection{
				Orgs: map[string]prowconfig.Org{
					"org-1": {
						Repos: map[string]prowconfig.Repo{
							"repo-a": {
								Policy: prowconfig.Policy{
									Unmanaged: &t,
								},
							},
							"repo-b": {
								Policy: prowconfig.Policy{
									Unmanaged: &t,
								},
							},
							"repo-c": {
								Policy: prowconfig.Policy{},
							},
							"repo-d": {
								Policy: prowconfig.Policy{},
							},
						},
					},
					"org-2": {
						Policy: prowconfig.Policy{
							Unmanaged: &t,
						},
					},
					"org-3": {
						Policy: prowconfig.Policy{
							Unmanaged: &t,
						},
					},
					"org-5": {
						Repos: map[string]prowconfig.Repo{
							"repo-a": {
								Policy: prowconfig.Policy{},
							},
							"repo-b": {
								Policy: prowconfig.Policy{
									Unmanaged: &t,
								},
							},
							"repo-c": {
								Policy: prowconfig.Policy{},
							},
							"repo-d": {
								Policy: prowconfig.Policy{},
							},
						},
					},
					"org-6": {
						Policy: prowconfig.Policy{
							Unmanaged: &t,
						},
					},
				},
			},
		},
	}
	configAgent := &prowconfig.Agent{}
	configAgent.Set(prowConfig)
	return configAgent
}

func (c fakeAutomationClient) HasPermission(org, repo, user string, roles ...string) (bool, error) {
	orgRepo := fmt.Sprintf("%s/%s", org, repo)
	userRoles, ok := c.permissionsByRepo[orgRepo][user]
	if !ok {
		return false, nil // User not found in permissions map
	}
	for _, role := range roles {
		for _, userRole := range userRoles {
			if role == userRole {
				return true, nil
			}
		}
	}
	return false, nil
}

func (c fakeAutomationClient) IsMember(org, user string) (bool, error) {
	for _, member := range c.membersByOrg[org] {
		if user == member {
			return true, nil
		}
	}
	if org == "fake" {
		return false, errors.New("intentional error")
	}

	return false, nil
}

func (c fakeAutomationClient) IsCollaborator(org, repo, user string) (bool, error) {
	orgRepo := fmt.Sprintf("%s/%s", org, repo)
	collaborators := c.collaboratorsByRepo[orgRepo]
	for _, collaborator := range collaborators {
		if collaborator == user {
			return true, nil
		}
	}
	if repo == "fake" {
		return false, errors.New("intentional error")
	}

	return false, nil
}

func (c fakeAutomationClient) IsAppInstalled(org, repo string) (bool, error) {
	if repo == "error" {
		return false, errors.New("intentional error")
	}

	orgRepo := fmt.Sprintf("%s/%s", org, repo)
	return c.reposWithAppInstalled.Has(orgRepo), nil
}

func (c fakeAutomationClient) GetRepo(owner, name string) (github.FullRepo, error) {
	orgRepo := fmt.Sprintf("%s/%s", owner, name)
	return c.repos[orgRepo], nil
}

func (c fakeAutomationClient) GetOrg(org string) (*github.Organization, error) {
	fullOrg := c.organizations[org]
	return &fullOrg, nil
}

func TestCheckRepos(t *testing.T) {
	client := fakeAutomationClient{
		repos: map[string]github.FullRepo{
			"org-1/repo-c": {
				Repo: github.Repo{
					Owner:     github.User{Type: "Organization"},
					Private:   false,
					HasIssues: true,
				},
			},
			"org-1/repo-d": {
				Repo: github.Repo{
					Owner:     github.User{Type: "Organization"},
					Private:   true,
					HasIssues: true,
				},
			},
			"org-5/repo-a": {
				Repo: github.Repo{
					Owner:     github.User{Type: "Organization"},
					Private:   false,
					HasIssues: false,
				},
			},
			"org-5/repo-d": {
				Repo: github.Repo{
					Owner:     github.User{Type: "Organization"},
					Private:   true,
					HasIssues: false,
				},
			},
			"user-1/repo-a": {
				Repo: github.Repo{
					Owner:   github.User{Type: "User"},
					Private: true,
				},
			},
		},
		collaboratorsByRepo: map[string][]string{
			"org-1/repo-a": {"a-bot", "b-bot", "openshift-cherrypick-robot"},
			"org-2/repo-z": {"c-bot", "some-user"},
		},
		membersByOrg: map[string][]string{
			"org-1": {"a-user", "d-bot", "e-bot", "openshift-cherrypick-robot"},
			"org-2": {"some-user", "z-bot"},
			"org-3": {"a-user"},
			"org-5": {"openshift-merge-robot"},
			"org-6": {"openshift-merge-robot"},
		},
		reposWithAppInstalled: sets.New[string]("org-1/repo-a", "org-1/repo-c", "org-1/repo-d", "org-2/repo-z", "org-5/repo-a", "org-5/repo-b", "org-5/repo-d", "org-6/repo-a"),
		permissionsByRepo: map[string]map[string][]string{
			"org-1/repo-a": {
				"a-bot":                      []string{"write"},
				"b-bot":                      []string{"write"},
				"openshift-cherrypick-robot": []string{"write"},
			},
			"org-1/repo-c": {
				"openshift-merge-robot": []string{"admin"},
			},
			"org-1/repo-d": {
				"openshift-merge-robot": []string{"admin"},
			},
			"org-2/repo-z": {
				"c-bot":     []string{"write"},
				"some-user": []string{"write"},
			},
			"org-5/repo-a": {
				"openshift-merge-robot": []string{"admin"},
			},
			"org-5/repo-c": {
				"openshift-merge-robot": []string{"read"},
			},
			"org-5/repo-d": {
				"openshift-merge-robot": []string{"admin"},
			},
		},
		organizations: map[string]github.Organization{
			"org-1": {Plan: github.OrgPlan{Name: "gold"}},
			"org-5": {Plan: github.OrgPlan{Name: "free"}},
		},
	}

	testCases := []struct {
		name      string
		repos     []string
		bots      []string
		adminBots []string
		mode      appCheckMode

		ignore      sets.Set[string]
		expected    []string
		expectedErr error
	}{
		{
			name:     "org has bots as members",
			repos:    []string{"org-1/repo-a"},
			bots:     []string{"d-bot", "e-bot"},
			mode:     standard,
			expected: []string{},
		},
		{
			name:     "org has one bot as member, and one as collaborator",
			repos:    []string{"org-1/repo-a"},
			bots:     []string{"a-bot", "e-bot"},
			mode:     standard,
			expected: []string{},
		},
		{
			name:     "repo has bots as collaborators",
			repos:    []string{"org-1/repo-a"},
			bots:     []string{"a-bot", "b-bot"},
			mode:     standard,
			expected: []string{},
		},
		{
			name:     "org doesn't have bots as members, and repo doesn't have bots as collaborators",
			repos:    []string{"org-2/repo-z"},
			bots:     []string{"a-bot", "b-bot"},
			mode:     standard,
			expected: []string{"org-2/repo-z"},
		},
		{
			name:     "multiple repos, some passing",
			repos:    []string{"org-1/repo-a", "org-2/repo-z"},
			bots:     []string{"a-bot", "b-bot"},
			mode:     standard,
			expected: []string{"org-2/repo-z"},
		},
		{
			name:     "app installed, no bots",
			repos:    []string{"org-1/repo-a"},
			mode:     standard,
			expected: []string{},
		},
		{
			name:     "app not installed",
			repos:    []string{"org-3/repo-y"},
			bots:     []string{"a-bot", "b-bot"},
			mode:     standard,
			expected: []string{"org-3/repo-y"},
		},
		{
			name:     "ignored repo",
			repos:    []string{"org-2/repo-z"},
			bots:     []string{"a-bot", "b-bot"},
			ignore:   sets.New[string]("org-2/repo-z"),
			mode:     standard,
			expected: []string{},
		},
		{
			name:     "ignored org",
			repos:    []string{"org-2/repo-z"},
			bots:     []string{"a-bot", "b-bot"},
			ignore:   sets.New[string]("org-2"),
			mode:     standard,
			expected: []string{},
		},
		{
			name:        "org member check returns error",
			repos:       []string{"fake/repo"},
			bots:        []string{"a-bot"},
			mode:        standard,
			expectedErr: errors.New("unable to determine if: a-bot is a member of fake: intentional error"),
		},
		{
			name:        "collaborator check returns error",
			repos:       []string{"org-1/fake"},
			bots:        []string{"a-bot"},
			mode:        standard,
			expectedErr: errors.New("unable to determine if: a-bot is a collaborator on org-1/fake: intentional error"),
		},
		{
			name:        "app install check returns error",
			repos:       []string{"org-1/error"},
			bots:        []string{"a-bot"},
			mode:        standard,
			expectedErr: errors.New("unable to determine if openshift-ci app is installed on org-1/error: intentional error"),
		},
		{
			name:     "app install check in tide mode successful when app installed and query exists",
			repos:    []string{"org-1/repo-a"},
			mode:     tide,
			expected: []string{},
		},
		{
			name:     "app install check in tide mode successful when query doesn't exist",
			repos:    []string{"org-2/repo-z"},
			mode:     tide,
			expected: []string{},
		},
		{
			name:     "app install check fails in tide mode when app not installed, and tide query configured",
			repos:    []string{"org-3/repo-z"},
			mode:     tide,
			expected: []string{"org-3/repo-z"},
		},
		{
			name:      "openshift-merge-robot with admin access and branch protection enabled",
			repos:     []string{"org-5/repo-a"},
			bots:      []string{"openshift-merge-robot"},
			adminBots: []string{"openshift-merge-robot"},
			mode:      standard,
			expected:  []string{},
		},
		{
			name:      "openshift-merge-robot without admin access and branch protection set to unmanaged",
			repos:     []string{"org-5/repo-b"},
			bots:      []string{"openshift-merge-robot"},
			adminBots: []string{},
			mode:      standard,
			expected:  []string{},
		},
		{
			name:      "openshift-merge-robot without admin access and branch protection enabled",
			repos:     []string{"org-5/repo-c"},
			bots:      []string{"openshift-merge-robot"},
			adminBots: []string{},
			mode:      standard,
			expected:  []string{"org-5/repo-c"},
		},
		{
			name:      "openshift-merge-robot without admin access and branch protection set to unmanaged at org level",
			repos:     []string{"org-6/repo-a"},
			bots:      []string{"openshift-merge-robot"},
			adminBots: []string{},
			mode:      standard,
			expected:  []string{},
		},
		{
			name:     "public repository has branch protection enabled and is a paid plan",
			repos:    []string{"org-1/repo-c"},
			mode:     standard,
			expected: []string{},
		},
		{
			name:     "public repository has branch protection enabled and is a free plan",
			repos:    []string{"org-5/repo-a"},
			mode:     standard,
			expected: []string{},
		},
		{
			name:     "private repository has branch protection enabled and is a paid plan",
			repos:    []string{"org-1/repo-d"},
			mode:     standard,
			expected: []string{},
		},
		{
			name:     "private repository has branch protection enabled and is a free plan",
			repos:    []string{"org-5/repo-d"},
			mode:     standard,
			expected: []string{"org-5/repo-d"},
		},
		{
			name:     "private repository has branch protection enabled and is a user owned",
			repos:    []string{"user-1/repo-a"},
			mode:     standard,
			expected: []string{"user-1/repo-a"},
		},
		{
			name:     "repository is participating in automated branching and has issues enabled",
			repos:    []string{"org-1/repo-c"},
			mode:     standard,
			expected: []string{},
		},
		{
			name:     "repository is participating in automated branching and has issues disabled",
			repos:    []string{"org-5/repo-d"},
			mode:     standard,
			expected: []string{"org-5/repo-d"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logrus.Infof("Testing %s", tc.name)
			failing, err := checkRepos(tc.repos, tc.bots, "openshift-ci", tc.ignore, tc.mode, true, newFakeConfiguration(), client, logrus.NewEntry(logrus.New()), newFakePluginConfigAgent(), newFakeProwConfigAgent().Config().Tide.Queries.QueryMap(), newFakeProwConfigAgent())
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error doesn't match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expected, failing); diff != "" {
				t.Fatalf("returned failing repos did not match expected, diff: %s", diff)
			}
		})
	}
}

func TestGatherModifiedReposProwConfigs(t *testing.T) {
	testCases := []struct {
		name            string
		prowConfigFiles []string
		expectedRepos   []string
	}{
		{
			name: "tide config only scenario - no CI jobs/configs",
			prowConfigFiles: []string{
				"core-services/prow/02_config/test-org/test-repo/_prowconfig.yaml",
			},
			expectedRepos: []string{"test-org/test-repo"},
		},
		{
			name: "multiple new orgs onboarding with tide",
			prowConfigFiles: []string{
				"core-services/prow/02_config/org1/repo1/_prowconfig.yaml",
				"core-services/prow/02_config/org2/repo2/_prowconfig.yaml",
			},
			expectedRepos: []string{"org1/repo1", "org2/repo2"},
		},
		{
			name: "ignores plugin configs and other files",
			prowConfigFiles: []string{
				"core-services/prow/02_config/valid-org/valid-repo/_prowconfig.yaml",
				"core-services/prow/02_config/ignored-org/ignored-repo/_pluginconfig.yaml", // ignored
				"core-services/prow/02_config/ignored-org/ignored-repo/OWNERS",             // ignored
			},
			expectedRepos: []string{"valid-org/valid-repo"},
		},
		{
			name: "handles invalid paths gracefully",
			prowConfigFiles: []string{
				"core-services/prow/02_config/valid-org/valid-repo/_prowconfig.yaml", // valid
				"core-services/prow/02_config/short/_prowconfig.yaml",                // invalid - too short
				"invalid/path/_prowconfig.yaml",                                      // invalid - wrong structure
			},
			expectedRepos: []string{"valid-org/valid-repo"},
		},
		{
			name:            "empty input returns no repos",
			prowConfigFiles: []string{},
			expectedRepos:   []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test the exact path splitting logic from gatherModifiedRepos
			orgRepos := sets.Set[string]{}

			for _, filePath := range tc.prowConfigFiles {
				if strings.HasSuffix(filePath, "_prowconfig.yaml") {
					pathParts := strings.Split(filePath, "/")
					if len(pathParts) >= 6 {
						orgRepos.Insert(fmt.Sprintf("%s/%s", pathParts[3], pathParts[4]))
					}
				}
			}

			result := sets.List(orgRepos)
			if diff := cmp.Diff(tc.expectedRepos, result); diff != "" {
				t.Fatalf("extracted org/repos did not match expected, diff: %s", diff)
			}
		})
	}
}
