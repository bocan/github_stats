package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// GitHubClient fetches and caches GitHub data for a single user.
type GitHubClient struct {
	token      string
	username   string
	httpClient *http.Client
	cache      map[string]cacheEntry
	mu         sync.RWMutex
	cacheTTL   time.Duration
}

type cacheEntry struct {
	data      interface{}
	expiresAt time.Time
}

func NewGitHubClient(token, username string, cacheTTL time.Duration) *GitHubClient {
	return &GitHubClient{
		token:    token,
		username: username,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		cache:    make(map[string]cacheEntry),
		cacheTTL: cacheTTL,
	}
}

func (c *GitHubClient) getCached(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.cache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.data, true
}

func (c *GitHubClient) setCached(key string, data interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = cacheEntry{
		data:      data,
		expiresAt: time.Now().Add(c.cacheTTL),
	}
}

// ---- Data types ----

type UserStats struct {
	Name          string
	Login         string
	Followers     int
	Following     int
	PublicRepos   int
	TotalStars    int
	Contributions int // all-time
	PullReqs      int
	Issues        int
}

type LangEntry struct {
	Name       string
	Percentage float64
	Color      string
}



// ---- REST helpers ----

type restUser struct {
	Name        string `json:"name"`
	Login       string `json:"login"`
	Followers   int    `json:"followers"`
	Following   int    `json:"following"`
	PublicRepos int    `json:"public_repos"`
}

type restRepo struct {
	StargazersCount int    `json:"stargazers_count"`
	Language        string `json:"language"`
	Fork            bool   `json:"fork"`
}

func (c *GitHubClient) restGet(path string, v interface{}) error {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub REST %d: %s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (c *GitHubClient) fetchAllRepos() ([]restRepo, error) {
	var all []restRepo
	for page := 1; ; page++ {
		var repos []restRepo
		path := fmt.Sprintf("/users/%s/repos?per_page=100&page=%d&type=owner", c.username, page)
		if err := c.restGet(path, &repos); err != nil {
			return nil, err
		}
		all = append(all, repos...)
		if len(repos) < 100 {
			break
		}
	}
	return all, nil
}

// fetchAllReposAuthenticated fetches all repos the authenticated user can see:
// personal (public + private) and all org repos they're a member of.
// Requires the PAT to have `repo` scope.
func (c *GitHubClient) fetchAllReposAuthenticated() ([]restRepo, error) {
	// Personal repos (includes private)
	var all []restRepo
	for page := 1; ; page++ {
		var repos []restRepo
		path := fmt.Sprintf("/user/repos?per_page=100&page=%d&affiliation=owner,collaborator&visibility=all", page)
		if err := c.restGet(path, &repos); err != nil {
			return nil, err
		}
		all = append(all, repos...)
		if len(repos) < 100 {
			break
		}
	}

	// Org repos
	var orgs []struct {
		Login string `json:"login"`
	}
	if err := c.restGet("/user/orgs?per_page=100", &orgs); err != nil {
		// Non-fatal: if the PAT lacks org scope just skip org repos
		return all, nil
	}
	for _, org := range orgs {
		for page := 1; ; page++ {
			var repos []restRepo
			path := fmt.Sprintf("/orgs/%s/repos?per_page=100&page=%d&type=member", org.Login, page)
			if err := c.restGet(path, &repos); err != nil {
				break // skip this org on error and continue
			}
			all = append(all, repos...)
			if len(repos) < 100 {
				break
			}
		}
	}
	return all, nil
}

// ---- GraphQL helper ----

type gqlRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *GitHubClient) graphQL(query string, vars map[string]interface{}, out interface{}) error {
	body, err := json.Marshal(gqlRequest{Query: query, Variables: vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.github.com/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var gqlResp gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return err
	}
	if len(gqlResp.Errors) > 0 {
		return fmt.Errorf("GraphQL: %s", gqlResp.Errors[0].Message)
	}
	return json.Unmarshal(gqlResp.Data, out)
}

// ---- FetchStats ----

func (c *GitHubClient) FetchStats() (UserStats, error) {
	if cached, ok := c.getCached("stats"); ok {
		return cached.(UserStats), nil
	}

	var user restUser
	if err := c.restGet("/users/"+c.username, &user); err != nil {
		return UserStats{}, fmt.Errorf("fetching user: %w", err)
	}

	repos, err := c.fetchAllRepos()
	if err != nil {
		return UserStats{}, fmt.Errorf("fetching repos: %w", err)
	}
	stars := 0
	for _, r := range repos {
		stars += r.StargazersCount
	}

	// Fetch join year so we can sum contributions from account creation to now.
	const userQuery = `
	query($login: String!) {
	  user(login: $login) {
	    createdAt
	  }
	}`
	var userData struct {
		User struct {
			CreatedAt string `json:"createdAt"`
		} `json:"user"`
	}
	if err := c.graphQL(userQuery, map[string]interface{}{"login": c.username}, &userData); err != nil {
		return UserStats{}, fmt.Errorf("fetching account creation date: %w", err)
	}
	joinYear := time.Now().Year()
	if t, err := time.Parse(time.RFC3339, userData.User.CreatedAt); err == nil {
		joinYear = t.Year()
	}

	// Also grab PRs and issues from the most recent year's contributionsCollection.
	const recentQuery = `
	query($login: String!) {
	  user(login: $login) {
	    contributionsCollection {
	      totalPullRequestContributions
	      totalIssueContributions
	    }
	  }
	}`
	var recentData struct {
		User struct {
			ContributionsCollection struct {
				TotalPullRequestContributions int `json:"totalPullRequestContributions"`
				TotalIssueContributions       int `json:"totalIssueContributions"`
			} `json:"contributionsCollection"`
		} `json:"user"`
	}
	if err := c.graphQL(recentQuery, map[string]interface{}{"login": c.username}, &recentData); err != nil {
		return UserStats{}, fmt.Errorf("fetching recent contributions: %w", err)
	}

	// Sum all-time contributions year by year.
	currentYear := time.Now().Year()
	totalContribs := 0
	for yr := joinYear; yr <= currentYear; yr++ {
		_, total, err := c.fetchYearDays(yr)
		if err != nil {
			return UserStats{}, fmt.Errorf("fetching contributions for %d: %w", yr, err)
		}
		totalContribs += total
	}

	stats := UserStats{
		Name:          user.Name,
		Login:         user.Login,
		Followers:     user.Followers,
		Following:     user.Following,
		PublicRepos:   user.PublicRepos,
		TotalStars:    stars,
		Contributions: totalContribs,
		PullReqs:      recentData.User.ContributionsCollection.TotalPullRequestContributions,
		Issues:        recentData.User.ContributionsCollection.TotalIssueContributions,
	}
	c.setCached("stats", stats)
	return stats, nil
}

// ---- FetchLangs ----

var langColors = map[string]string{
	"Go":         "#00ADD8",
	"Python":     "#3572A5",
	"JavaScript": "#F1E05A",
	"TypeScript": "#3178C6",
	"Rust":       "#DEA584",
	"Java":       "#B07219",
	"C":          "#555555",
	"C++":        "#F34B7D",
	"C#":         "#178600",
	"Ruby":       "#701516",
	"PHP":        "#4F5D95",
	"Swift":      "#F05138",
	"Kotlin":     "#A97BFF",
	"Shell":      "#89E051",
	"HTML":       "#E34C26",
	"CSS":        "#563D7C",
	"Dockerfile": "#384D54",
	"HCL":        "#844FBA",
	"Makefile":   "#427819",
	"Nix":        "#7e7eff",
}

func langColor(name string) string {
	if col, ok := langColors[name]; ok {
		return col
	}
	hash := 0
	for _, ch := range name {
		hash = hash*31 + int(ch)
	}
	return fmt.Sprintf("#%06X", hash&0xFFFFFF)
}

func (c *GitHubClient) FetchLangs() ([]LangEntry, error) {
	if cached, ok := c.getCached("langs"); ok {
		return cached.([]LangEntry), nil
	}

	repos, err := c.fetchAllReposAuthenticated()
	if err != nil {
		return nil, fmt.Errorf("fetching repos: %w", err)
	}

	counts := make(map[string]int)
	total := 0
	for _, r := range repos {
		if r.Language != "" && !r.Fork {
			counts[r.Language]++
			total++
		}
	}

	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range counts {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
	if len(sorted) > 6 {
		sorted = sorted[:6]
	}

	var entries []LangEntry
	for _, item := range sorted {
		pct := 0.0
		if total > 0 {
			pct = float64(item.v) / float64(total) * 100
		}
		entries = append(entries, LangEntry{
			Name:       item.k,
			Percentage: pct,
			Color:      langColor(item.k),
		})
	}

	c.setCached("langs", entries)
	return entries, nil
}

// ---- FetchStreak ----

type contribDay struct {
	Date  string `json:"date"`
	Count int    `json:"contributionCount"`
}

// fetchYearDays fetches contribution days for a single calendar year.
func (c *GitHubClient) fetchYearDays(year int) ([]contribDay, int, error) {
	from := fmt.Sprintf("%d-01-01T00:00:00Z", year)
	to := fmt.Sprintf("%d-12-31T23:59:59Z", year)

	const query = `
	query($login: String!, $from: DateTime!, $to: DateTime!) {
	  user(login: $login) {
	    contributionsCollection(from: $from, to: $to) {
	      contributionCalendar {
	        totalContributions
	        weeks {
	          contributionDays {
	            date
	            contributionCount
	          }
	        }
	      }
	    }
	  }
	}`

	var data struct {
		User struct {
			ContributionsCollection struct {
				ContributionCalendar struct {
					TotalContributions int `json:"totalContributions"`
					Weeks              []struct {
						ContributionDays []contribDay `json:"contributionDays"`
					} `json:"weeks"`
				} `json:"contributionCalendar"`
			} `json:"contributionsCollection"`
		} `json:"user"`
	}

	if err := c.graphQL(query, map[string]interface{}{
		"login": c.username,
		"from":  from,
		"to":    to,
	}, &data); err != nil {
		return nil, 0, err
	}

	cal := data.User.ContributionsCollection.ContributionCalendar
	var days []contribDay
	for _, week := range cal.Weeks {
		days = append(days, week.ContributionDays...)
	}
	return days, cal.TotalContributions, nil
}
