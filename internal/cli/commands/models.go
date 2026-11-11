package commands

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"shunt/internal/clients"
	"shunt/internal/config"
	"shunt/internal/models"
	"shunt/internal/platform"
	"shunt/internal/state"
	"shunt/internal/upstream"
)

type modelOptions struct {
	via        string
	dialect    string
	refresh    bool
	json       bool
	forClient  string
	api        string
	model      string
	opus       string
	sonnet     string
	haiku      string
	fable      string
	legacy     bool
	write      bool
	dryRun     bool
	path       string
	tokenEnv   string
	pathWasSet bool
	force      bool
}

// Models implements `shunt models`.
func Models(e *Env, args []string) int {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	fs.SetOutput(e.err())
	opts := modelOptions{}
	fs.StringVar(&opts.via, "via", "active", "active or a profile id")
	fs.StringVar(&opts.dialect, "dialect", "", "anthropic or openai")
	fs.BoolVar(&opts.refresh, "refresh", false, "ignore a fresh cache")
	fs.BoolVar(&opts.json, "json", e.JSON, "print the normalized catalog as JSON")
	fs.StringVar(&opts.forClient, "for", "", "claude, codex, or dsh")
	fs.StringVar(&opts.api, "api", "both", "DSH: anthropic, openai, or both")
	fs.StringVar(&opts.model, "model", "", "explicit model id")
	fs.StringVar(&opts.opus, "opus", "", "Claude Code opus alias")
	fs.StringVar(&opts.sonnet, "sonnet", "", "Claude Code sonnet alias")
	fs.StringVar(&opts.haiku, "haiku", "", "Claude Code haiku alias")
	fs.StringVar(&opts.fable, "fable", "", "Claude Code fable alias")
	fs.BoolVar(&opts.legacy, "legacy", false, "emit ANTHROPIC_SMALL_FAST_MODEL")
	fs.BoolVar(&opts.write, "write", false, "write the generated configuration")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print proposed content without writing")
	fs.StringVar(&opts.path, "path", "", "override the client configuration destination")
	fs.StringVar(&opts.tokenEnv, "token-env", "", "environment variable holding the token")
	fs.BoolVar(&opts.force, "force", false, "overwrite existing client model keys")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "path" {
			opts.pathWasSet = true
		}
	})
	if opts.forClient != "" && opts.json {
		return e.fail(ExitUsage, "--for cannot be combined with --json")
	}
	if opts.write && opts.dryRun {
		return e.fail(ExitUsage, "--write cannot be combined with --dry-run")
	}
	switch opts.forClient {
	case "", "claude", "codex", "dsh":
	default:
		return e.fail(ExitUsage, "--for must be claude, codex, or dsh")
	}
	if opts.api != "anthropic" && opts.api != "openai" && opts.api != "both" {
		return e.fail(ExitUsage, "--api must be anthropic, openai, or both")
	}
	cfg, _, paths, err := e.load()
	if err != nil {
		return e.fail(ExitUsage, "%v", err)
	}
	snap, live := e.currentSnapshot(cfg, paths)
	effective := snap.Effective
	if effective == "" {
		if intent, err := e.readState(paths.StateFile); err == nil {
			effective = effectiveFromState(intent, cfg)
		}
	}
	if effective == "" {
		effective = cfg.DefaultProfile
	}
	viaRelay := opts.via == "" || opts.via == "active"
	if viaRelay && !live {
		return e.fail(ExitRelayNotRunning, "relay not running at %s; start shunt serve or use --via <profile>", cfg.Relay.Listen)
	}
	profileID := effective
	if !viaRelay {
		profileID = opts.via
	}
	profile, ok := cfg.Profiles[profileID]
	if !ok {
		return e.fail(ExitUsage, "profile %q does not exist", profileID)
	}
	if !profile.IsEnabled() {
		return e.fail(ExitUsage, "profile %q is disabled", profileID)
	}
	requestBase := profile.BaseURL
	if viaRelay {
		requestBase = relayOrigin(cfg)
	}
	token, tokenName := models.ResolveToken(e.tokenLookup(), opts.tokenEnv)
	_ = tokenName

	switch opts.forClient {
	case "claude":
		return modelsForClaude(e, cfg, paths, opts, profileID, profile.BaseURL, requestBase, token)
	case "codex":
		return modelsForCodex(e, cfg, paths, opts, profileID, profile.BaseURL, requestBase, token)
	case "dsh":
		return modelsForDSH(e, cfg, paths, opts, profileID, profile.BaseURL, requestBase, token)
	}

	dialect := opts.dialect
	if dialect == "" {
		dialect = cfg.Models.DefaultDialect
	}
	cat, err := fetchCatalog(e, cfg, paths, profileID, profile.BaseURL, requestBase, dialect, token, opts.refresh)
	if err != nil {
		return reportModelError(e, err)
	}
	if opts.json {
		if err := e.printJSON(cat); err != nil {
			return e.fail(ExitGeneric, "%v", err)
		}
		return ExitOK
	}
	printCatalog(e, cat)
	return ExitOK
}

func effectiveFromState(intent state.Intent, cfg config.Config) string {
	if intent.Mode == state.ModeAuto && intent.Preferred != nil {
		return *intent.Preferred
	}
	if intent.Selected != nil {
		return *intent.Selected
	}
	if intent.Preferred != nil {
		return *intent.Preferred
	}
	return cfg.DefaultProfile
}

func fetchCatalog(e *Env, cfg config.Config, paths platform.Paths, profileID, profileBase, requestBase, dialect, token string, refresh bool) (models.Catalog, error) {
	dialect = strings.ToLower(strings.TrimSpace(dialect))
	if dialect != models.DialectAnthropic && dialect != models.DialectOpenAI {
		return models.Catalog{}, fmt.Errorf("dialect must be anthropic or openai")
	}
	cache := models.NewCache(paths.CacheDir, cfg.Models.CacheTTLHours, cfg.Models.StaleTTLHours)
	sourceURL := models.ModelsURL(profileBase, dialect)
	if refresh {
		_ = cache.Invalidate(profileID, dialect)
	}
	var stale *models.Catalog
	if !refresh {
		cat, state, err := cache.Load(profileID, dialect, sourceURL)
		if state.Fresh {
			return cat, nil
		}
		if state.Usable {
			stale = &cat
		}
		if err != nil {
			fmt.Fprintf(e.err(), "warning: %v\n", err)
		}
	}
	yes := true
	requestProfile := config.Profile{
		BaseURL:                 requestBase,
		ConnectTimeoutMS:        cfg.Relay.ConnectTimeoutMS,
		ResponseHeaderTimeoutMS: cfg.Relay.ResponseHeaderTimeoutMS,
		TLSVerify:               &yes,
	}
	client := upstream.NewClient(requestProfile)
	if tr, ok := client.Transport.(*http.Transport); ok {
		defer tr.CloseIdleConnections()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cat, err := models.Fetch(ctx, models.FetchOptions{
		Profile:    profileID,
		BaseURL:    requestBase,
		Dialect:    dialect,
		Token:      token,
		HTTPClient: client,
		PageSize:   cfg.Models.PageSize,
		MaxPages:   cfg.Models.MaxPages,
		Source:     sourceName(requestBase, profileBase),
	})
	if err != nil {
		if stale != nil {
			reason := "fetch failed: " + err.Error()
			stale.StaleReason = &reason
			fmt.Fprintf(e.err(), "warning: %s; using cached catalog\n", reason)
			return *stale, nil
		}
		return models.Catalog{}, err
	}
	if err := cache.Save(cat); err != nil {
		fmt.Fprintf(e.err(), "warning: cache write failed: %v\n", err)
	}
	return cat, nil
}

func sourceName(requestBase, profileBase string) string {
	if requestBase == profileBase {
		return "direct"
	}
	return "relay"
}

func reportModelError(e *Env, err error) int {
	var fe *models.FetchError
	if errors.As(err, &fe) {
		switch fe.Kind {
		case models.KindAuth:
			return e.fail(ExitUpstream, "authentication failed for %s (HTTP %d); set SHUNT_TOKEN or the client's key variable", fe.Profile, fe.Status)
		case models.KindNotFound:
			return e.fail(ExitUpstream, "model listing not found at %s; the upstream may not implement /v1/models", fe.URL)
		case models.KindEmpty, models.KindParse, models.KindUnreachable, models.KindUpstream:
			return e.fail(ExitUpstream, "%v", err)
		}
	}
	return e.fail(ExitUpstream, "%v", err)
}

func printCatalog(e *Env, cat models.Catalog) {
	e.printf("PROFILE  %-12s DIALECT  %-10s FETCHED  %s\n", cat.Profile, cat.Dialect, cat.FetchedAt.UTC().Format(time.RFC3339))
	if cat.Stale {
		reason := ""
		if cat.StaleReason != nil {
			reason = " (" + *cat.StaleReason + ")"
		}
		e.printf("STALE%s\n", reason)
	}
	e.printf("%-32s %-5s %-9s %-8s %s\n", "MODEL", "FREE", "CONTEXT", "MAX OUT", "CAPS")
	for _, m := range cat.Models {
		free := "?"
		switch m.Free {
		case models.FreeFree:
			free = "FREE"
		case models.FreePaid:
			free = "PAID"
		}
		ctxWindow := "-"
		if m.ContextWindow != nil {
			ctxWindow = fmt.Sprintf("%d", *m.ContextWindow)
		}
		maxOut := "-"
		if m.MaxOutputTokens != nil {
			maxOut = fmt.Sprintf("%d", *m.MaxOutputTokens)
		}
		e.printf("%-32s %-5s %-9s %-8s %s\n", m.ID, free, ctxWindow, maxOut, strings.Join(m.Capabilities, ","))
	}
	e.printf("%d models: %d free, %d paid, %d unknown\n", cat.Counts.Total, cat.Counts.Free, cat.Counts.Paid, cat.Counts.Unknown)
}

func selectModel(e *Env, cat models.Catalog, explicit string) (models.Record, int) {
	if explicit != "" {
		for _, m := range cat.Models {
			if m.ID == explicit {
				if m.Free != models.FreeFree {
					fmt.Fprintf(e.err(), "warning: model %q is %s\n", m.ID, m.Free)
				}
				return m, ExitOK
			}
		}
		return models.Record{}, e.fail(ExitUsage, "model %q does not exist in the %s catalog for %s", explicit, cat.Dialect, cat.Profile)
	}
	for _, m := range cat.Models {
		if m.Free == models.FreeFree {
			return m, ExitOK
		}
	}
	ids := make([]string, 0, len(cat.Models))
	for _, m := range cat.Models {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	fmt.Fprintf(e.err(), "available models: %s\n", strings.Join(ids, ", "))
	return models.Record{}, e.fail(ExitNoFreeModel, "no free model in %s for %s; choose one with --model", cat.Dialect, cat.Profile)
}

func modelsForClaude(e *Env, cfg config.Config, paths platform.Paths, opts modelOptions, profileID, profileBase, requestBase, token string) int {
	dialect := opts.dialect
	if dialect == "" {
		dialect = models.DialectAnthropic
	}
	if dialect != models.DialectAnthropic {
		return e.fail(ExitUsage, "Claude Code requires --dialect anthropic")
	}
	cat, err := fetchCatalog(e, cfg, paths, profileID, profileBase, requestBase, dialect, token, opts.refresh)
	if err != nil {
		return reportModelError(e, err)
	}
	selected, code := selectModel(e, cat, opts.model)
	if code != ExitOK {
		return code
	}
	claude := clients.ClaudeOptions{
		BaseURL:    relayOrigin(cfg),
		Model:      selected.ID,
		Opus:       opts.opus,
		Sonnet:     opts.sonnet,
		Haiku:      opts.haiku,
		Fable:      opts.fable,
		Legacy:     opts.legacy,
		PowerShell: runtime.GOOS == "windows",
	}
	target, err := claudeTarget(cfg, opts)
	if err != nil {
		return e.fail(ExitWriteRefused, "%v", err)
	}
	if opts.write {
		if err := clients.EnsureInsideHome(target, opts.pathWasSet); err != nil {
			return e.fail(ExitWriteRefused, "%v", err)
		}
		var existing []byte
		if data, err := os.ReadFile(target); err == nil {
			existing = data
		}
		merged, changed, err := clients.MergeClaudeSettings(existing, claude)
		if err != nil {
			return e.fail(ExitWriteRefused, "%v", err)
		}
		if opts.dryRun {
			e.printf("%s", clients.DryRun(target, merged))
			return ExitOK
		}
		backup, err := clients.WriteWithBackup(paths.BackupDir, "claude", target, merged)
		if err != nil {
			return e.fail(ExitWriteRefused, "write %s: %v", target, err)
		}
		e.printf("target: %s\n", target)
		if backup != "" {
			e.printf("backup: %s\n", backup)
		}
		e.printf("changed: %s\n", strings.Join(changed, ", "))
		return ExitOK
	}
	if opts.dryRun {
		settings, err := clients.ClaudeSettings(claude)
		if err != nil {
			return e.fail(ExitGeneric, "%v", err)
		}
		e.printf("%s", clients.DryRun(target, settings))
		return ExitOK
	}
	e.printf("%s", clients.ClaudeShell(claude))
	return ExitOK
}

func claudeTarget(cfg config.Config, opts modelOptions) (string, error) {
	target := opts.path
	if target == "" {
		target = cfg.Clients.Claude.SettingsPath
	}
	if target == "" {
		target = "~/.claude/settings.json"
	}
	return platform.ExpandHome(target)
}

func modelsForCodex(e *Env, cfg config.Config, paths platform.Paths, opts modelOptions, profileID, profileBase, requestBase, token string) int {
	dialect := opts.dialect
	if dialect == "" {
		dialect = models.DialectOpenAI
	}
	if dialect != models.DialectOpenAI {
		return e.fail(ExitUsage, "Codex requires --dialect openai")
	}
	cat, err := fetchCatalog(e, cfg, paths, profileID, profileBase, requestBase, dialect, token, opts.refresh)
	if err != nil {
		return reportModelError(e, err)
	}
	selected, code := selectModel(e, cat, opts.model)
	if code != ExitOK {
		return code
	}
	codex := clients.CodexOptions{RelayOrigin: relayOrigin(cfg), Model: selected.ID, Force: opts.force}
	target, err := codexTarget(cfg, opts)
	if err != nil {
		return e.fail(ExitWriteRefused, "%v", err)
	}
	if opts.write {
		if err := clients.EnsureInsideHome(target, opts.pathWasSet); err != nil {
			return e.fail(ExitWriteRefused, "%v", err)
		}
		existing := ""
		if data, err := os.ReadFile(target); err == nil {
			existing = string(data)
		}
		merged, changed, err := clients.MergeCodex(existing, codex)
		if err != nil {
			return e.fail(ExitWriteRefused, "%v", err)
		}
		if opts.dryRun {
			e.printf("%s", clients.DryRun(target, []byte(merged)))
			return ExitOK
		}
		backup, err := clients.WriteWithBackup(paths.BackupDir, "codex", target, []byte(merged))
		if err != nil {
			return e.fail(ExitWriteRefused, "write %s: %v", target, err)
		}
		e.printf("target: %s\n", target)
		if backup != "" {
			e.printf("backup: %s\n", backup)
		}
		e.printf("changed: %s\n", strings.Join(changed, ", "))
		return ExitOK
	}
	snippet := clients.CodexSnippet(codex)
	if opts.dryRun {
		e.printf("%s", clients.DryRun(target, []byte(snippet)))
		return ExitOK
	}
	e.printf("%s", snippet)
	return ExitOK
}

func codexTarget(cfg config.Config, opts modelOptions) (string, error) {
	target := opts.path
	if target == "" {
		target = cfg.Clients.Codex.ConfigPath
	}
	if target == "" {
		target = "~/.codex/config.toml"
	}
	return platform.ExpandHome(target)
}

func modelsForDSH(e *Env, cfg config.Config, paths platform.Paths, opts modelOptions, profileID, profileBase, requestBase, token string) int {
	api := opts.api
	if api == "" {
		api = "both"
	}
	var anthropicCat, openaiCat *models.Catalog
	if api == "anthropic" || api == "both" {
		cat, err := fetchCatalog(e, cfg, paths, profileID, profileBase, requestBase, models.DialectAnthropic, token, opts.refresh)
		if err != nil {
			return reportModelError(e, err)
		}
		anthropicCat = &cat
	}
	if api == "openai" || api == "both" {
		cat, err := fetchCatalog(e, cfg, paths, profileID, profileBase, requestBase, models.DialectOpenAI, token, opts.refresh)
		if err != nil {
			return reportModelError(e, err)
		}
		openaiCat = &cat
	}
	dsh := clients.DSHOptions{RelayOrigin: relayOrigin(cfg), API: api}
	if anthropicCat != nil {
		m, code := selectModel(e, *anthropicCat, opts.model)
		if code != ExitOK {
			return code
		}
		dsh.AnthropicID, dsh.AnthropicName = m.ID, m.DisplayName
		if m.ContextWindow != nil {
			dsh.AnthropicCtx = *m.ContextWindow
		}
	}
	if openaiCat != nil {
		m, code := selectModel(e, *openaiCat, opts.model)
		if code != ExitOK {
			return code
		}
		dsh.OpenAIID, dsh.OpenAIName = m.ID, m.DisplayName
		if m.ContextWindow != nil {
			dsh.OpenAICtx = *m.ContextWindow
		}
	}
	if anthropicCat != nil && openaiCat != nil && opts.model == "" {
		common := commonFree(*anthropicCat, *openaiCat)
		if common != "" {
			dsh.AnthropicID, dsh.OpenAIID = common, common
			dsh.AnthropicName, dsh.OpenAIName = common, common
		} else {
			fmt.Fprintf(e.err(), "warning: no free model is present in both dialects; each provider uses its first free model\n")
		}
	}
	fragment := clients.DSHFragment(dsh)
	if opts.write {
		e.printf("%s", fragment)
		return e.fail(ExitWriteRefused, "DSH writes are not supported in v1; merge the fragment above into the llm-pi-ai row")
	}
	if opts.dryRun {
		e.printf("%s", clients.DryRun("dsh cordis.patch.yml", []byte(fragment)))
		return ExitOK
	}
	e.printf("%s", fragment)
	return ExitOK
}

func commonFree(a, b models.Catalog) string {
	freeB := map[string]bool{}
	for _, m := range b.Models {
		if m.Free == models.FreeFree {
			freeB[m.ID] = true
		}
	}
	for _, m := range a.Models {
		if m.Free == models.FreeFree && freeB[m.ID] {
			return m.ID
		}
	}
	return ""
}
