package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The page "AI assistants (MCP)" and its REST twin (ticket 7.7, ADR 0015
// parity, preview-96): a switch, the MCP address, and the tokens made for MCP.

const mcpPath = "/mcp"

// mcpTokens lists the household's tokens made for MCP, oldest first.
func (s *server) mcpTokens(householdID string) ([]store.APIToken, error) {
	tokens, err := s.db.APITokens(householdID)
	if err != nil {
		return nil, err
	}
	var out []store.APIToken
	for _, t := range tokens {
		if t.MCP {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *server) mcpAddress(r *http.Request) string { return s.httpsOrigin(r) + mcpPath }

// areaNames says, in the reader's language, which areas of a token have each
// level: changing, reading only, and none.
func (s *server) areaNames(r *http.Request, scopes string) (writes, reads, denied []string) {
	access := auth.MCPAccess(scopes)
	for _, area := range auth.MCPAreas {
		name := s.say(r, i18n.Msg("mcp.area."+area))
		switch access[area] {
		case auth.MCPWrite:
			writes = append(writes, name)
		case auth.MCPRead:
			reads = append(reads, name)
		default:
			denied = append(denied, name)
		}
	}
	return writes, reads, denied
}

// mcpOverview is the page: the switch and address, then the tokens.
func (s *server) renderMCP(w http.ResponseWriter, r *http.Request, p principal, status int) {
	tokens, err := s.mcpTokens(p.household.ID)
	if err != nil {
		s.fail(w, "mcp tokens", err)
		return
	}
	chips, err := s.chipByPerson(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	var rows []rowView
	for _, t := range tokens {
		row := rowView{Href: mcpPath + "/tokens/" + t.ID, Title: t.Name, Lines: []rowLine{s.whoLine(r, chips, t.PersonID, t.PersonName)}}
		writes, reads, _ := s.areaNames(r, t.Scopes)
		if len(writes) > 0 {
			row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("mcp.row.writes", "areas", strings.Join(writes, ", ")))})
		}
		if len(reads) > 0 {
			row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("mcp.row.reads", "areas", strings.Join(reads, ", ")))})
		}
		switch {
		case !p.household.MCPEnabled:
			row.Flag = &flagView{Kind: "warn", Text: s.say(r, i18n.Msg("mcp.row.off"))}
		case t.LastUsed.IsZero():
			row.Flag = &flagView{Kind: "warn", Text: s.say(r, i18n.Msg("mcp.row.never"))}
		default:
			row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("mcp.row.last_used", "when", p.when(t.LastUsed)))})
		}
		rows = append(rows, row)
	}
	s.render(w, r, status, "mcp.html", map[string]any{
		"Person": p.person, "Enabled": p.household.MCPEnabled, "Address": s.mcpAddress(r), "Rows": rows,
		// With the box's own CA the device the assistant runs on must trust the box first (ADR 0014).
		"CAFirst": s.pki.Ready() && !s.publicInUse(),
	})
}

func (s *server) handleMCPPage(w http.ResponseWriter, r *http.Request) {
	// An MCP client may ask for the event stream at the same address.
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		s.handleMCPEndpoint(w, r)
		return
	}
	if p, ok := s.parentPage(w, r); ok {
		s.renderMCP(w, r, p, http.StatusOK)
	}
}

// handleMCPPost tells the settings form from an MCP message: a form is the
// page's switch, everything else (JSON-RPC) is the protocol.
func (s *server) handleMCPPost(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
		s.handleMCPSave(w, r)
		return
	}
	s.handleMCPEndpoint(w, r)
}

func (s *server) handleMCPSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	on := r.PostFormValue("mcp") == "on"
	if err := s.db.SetMCPEnabled(p.household.ID, on); err != nil {
		s.fail(w, "switch mcp", err)
		return
	}
	key := "flash.mcp_off"
	if on {
		key = "flash.mcp_on"
	}
	s.redirectSaved(w, r, mcpPath, key)
}

// mcpAreaView is one area of the new-token form.
type mcpAreaView struct{ Area, Name, Desc, Level string }

func (s *server) mcpAreaViews(r *http.Request, levels map[string]string) []mcpAreaView {
	out := make([]mcpAreaView, 0, len(auth.MCPAreas))
	for _, area := range auth.MCPAreas {
		level := levels[area]
		if level == "" {
			level = string(auth.MCPRead) // Q10 (15/11/2026): new tokens start read only
		}
		out = append(out, mcpAreaView{area, s.say(r, i18n.Msg("mcp.area."+area)), s.say(r, i18n.Msg("mcp.area."+area+".desc")), level})
	}
	return out
}

func (s *server) renderMCPNew(w http.ResponseWriter, r *http.Request, p principal, status int, levels map[string]string, name, errKey, errField string, scopesError bool) {
	chips, err := s.chipByPerson(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	s.render(w, r, status, "mcp_token_new.html", map[string]any{
		"Person": p.person, "Who": chips[p.person.ID], "Areas": s.mcpAreaViews(r, levels), "Name": name, "Error": errKey, "ErrorField": errField, "ScopesError": scopesError,
	})
}

func (s *server) handleMCPTokenNew(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if !p.household.MCPEnabled { // no new token while MCP is off
		http.Redirect(w, r, mcpPath, http.StatusSeeOther)
		return
	}
	s.renderMCPNew(w, r, p, http.StatusOK, nil, "", "", "", false)
}

// Reasons a new MCP token is refused; each is a 422 with its own text.
var (
	errMCPName      = errors.New("mcp: a token needs a name")
	errMCPNameLong  = errors.New("mcp: the name is at most 80 characters")
	errMCPNameTaken = errors.New("mcp: there is a token with this name")
	errMCPScopes    = errors.New("mcp: a token needs at least one area")
)

// checkMCPToken says what is wrong with a new token: its name (empty, too
// long or taken) and its areas (none given, or an unknown level) are checked
// apart, so a form can show both at once.
func (s *server) checkMCPToken(p principal, name string, levels map[string]auth.MCPLevel) (nameErr, scopesErr error, err error) {
	switch {
	case name == "":
		nameErr = errMCPName
	case !validDeviceName(name):
		nameErr = errMCPNameLong
	default:
		existing, err := s.mcpTokens(p.household.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, t := range existing {
			if strings.EqualFold(t.Name, name) {
				nameErr = errMCPNameTaken
			}
		}
	}
	if _, err := auth.MCPScopes(levels); err != nil {
		scopesErr = errMCPScopes
	}
	return nameErr, scopesErr, nil
}

// createMCPToken makes a token for MCP for the parent p: the areas become the
// token scopes, and the secret is returned once.
func (s *server) createMCPToken(p principal, name string, levels map[string]auth.MCPLevel) (store.APIToken, string, error) {
	nameErr, scopesErr, err := s.checkMCPToken(p, name, levels)
	if err != nil {
		return store.APIToken{}, "", err
	}
	if nameErr != nil {
		return store.APIToken{}, "", nameErr
	}
	if scopesErr != nil {
		return store.APIToken{}, "", scopesErr
	}
	scopes, _ := auth.MCPScopes(levels)
	value := auth.NewToken()
	t, err := s.db.CreateAPIToken(store.APIToken{Name: name, Hash: auth.HashToken(value), PersonID: p.person.ID, Scopes: scopes, MCP: true})
	return t, value, err
}

// mcpError gives the text of a refused name and the field it belongs to.
func mcpError(err error) (key, field string) {
	switch {
	case errors.Is(err, errMCPNameTaken):
		return "mcp.error.name_taken", "name"
	case errors.Is(err, errMCPNameLong):
		return "mcp.error.name_long", "name"
	}
	return "mcp.error.name", "name"
}

func (s *server) handleMCPTokenCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if !p.household.MCPEnabled {
		http.Redirect(w, r, mcpPath, http.StatusSeeOther)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	posted, levels := map[string]string{}, map[string]auth.MCPLevel{}
	for _, area := range auth.MCPAreas {
		v := r.PostFormValue(area)
		if v == "" {
			v = string(auth.MCPNone)
		}
		posted[area], levels[area] = v, auth.MCPLevel(v)
	}
	nameErr, scopesErr, err := s.checkMCPToken(p, name, levels)
	if err != nil {
		s.fail(w, "check mcp token", err)
		return
	}
	if nameErr != nil || scopesErr != nil { // both are shown at once, each at its own place
		key, field := "", ""
		if nameErr != nil {
			key, field = mcpError(nameErr)
		}
		s.renderMCPNew(w, r, p, http.StatusUnprocessableEntity, posted, name, key, field, scopesErr != nil)
		return
	}
	t, value, err := s.createMCPToken(p, name, levels)
	if err != nil {
		s.fail(w, "create mcp token", err)
		return
	}
	writes, reads, _ := s.areaNames(r, t.Scopes)
	// The secret is shown on this answer only: no redirect, nothing stored but its hash.
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, "mcp_token_once.html", map[string]any{
		"Person": p.person, "Name": name, "Token": value, "ID": t.ID, "Address": s.mcpAddress(r),
		"Writes": strings.Join(writes, ", "), "Reads": strings.Join(reads, ", "),
		"SuccessText": s.say(r, i18n.Msg("flash.mcp_token_created", "name", name)),
	})
}

// mcpToken loads one MCP token of the household; false when there is none
// (an ordinary token or a device token is not found here).
func (s *server) mcpToken(w http.ResponseWriter, r *http.Request, p principal) (store.APIToken, bool) {
	t, err := s.db.APITokenByID(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !t.MCP) {
		http.NotFound(w, r)
		return store.APIToken{}, false
	}
	if err != nil {
		s.fail(w, "mcp token", err)
		return store.APIToken{}, false
	}
	return t, true
}

func (s *server) handleMCPTokenPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	t, ok := s.mcpToken(w, r, p)
	if !ok {
		return
	}
	chips, err := s.chipByPerson(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	writes, reads, denied := s.areaNames(r, t.Scopes)
	who := factView{Label: s.say(r, i18n.Msg("mcp.fact.for"))}
	if c, ok := chips[t.PersonID]; ok {
		who.People = []personChip{c}
	} else {
		who.Value = t.PersonName
	}
	last := factView{Label: s.say(r, i18n.Msg("mcp.fact.last_used"))}
	if t.LastUsed.IsZero() {
		last.Flag = &flagView{Kind: "warn", Text: s.say(r, i18n.Msg("mcp.row.never"))}
	} else {
		last.Value = p.when(t.LastUsed)
	}
	facts := []factView{who}
	for _, f := range []struct {
		key   string
		areas []string
	}{{"mcp.fact.writes", writes}, {"mcp.fact.reads", reads}, {"mcp.fact.denied", denied}} {
		if len(f.areas) > 0 {
			facts = append(facts, factView{Label: s.say(r, i18n.Msg(f.key)), Value: strings.Join(f.areas, ", ")})
		}
	}
	facts = append(facts, factView{Label: s.say(r, i18n.Msg("mcp.fact.created")), Value: p.when(t.Created)}, last)
	s.render(w, r, http.StatusOK, "mcp_token.html", map[string]any{
		"Person": p.person, "Token": t, "Title": t.Name, "Facts": facts, "Off": !p.household.MCPEnabled,
		"StateLabel": s.say(r, i18n.Msg("mcp.fact.state")),
	})
}

func (s *server) handleMCPTokenRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	t, ok := s.mcpToken(w, r, p)
	if !ok {
		return
	}
	if err := s.db.RevokeAPIToken(t.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "revoke mcp token", err)
		return
	}
	s.redirectSavedNamed(w, r, mcpPath, "flash.token_revoked", t.Name)
}

// REST: GET and PUT /mcp, POST /mcp/tokens (ADR 0015: whatever the page does).

type mcpJSON struct {
	Enabled bool   `json:"enabled"`
	Address string `json:"address"`
}

func (s *server) apiGetMCP(w http.ResponseWriter, r *http.Request, p principal) {
	writeJSON(w, http.StatusOK, mcpJSON{p.household.MCPEnabled, s.mcpAddress(r)})
}

func (s *server) apiSetMCP(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Enabled == nil {
		writeError(w, http.StatusUnprocessableEntity, "enabled is required")
		return
	}
	if err := s.db.SetMCPEnabled(p.household.ID, *in.Enabled); err != nil {
		s.fail(w, "switch mcp", err)
		return
	}
	writeJSON(w, http.StatusOK, mcpJSON{*in.Enabled, s.mcpAddress(r)})
}

func (s *server) apiCreateMCPToken(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name  string                   `json:"name"`
		Areas map[string]auth.MCPLevel `json:"areas"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	t, value, err := s.createMCPToken(p, strings.TrimSpace(in.Name), in.Areas)
	switch {
	case errors.Is(err, errMCPName):
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	case errors.Is(err, errMCPNameLong):
		writeError(w, http.StatusUnprocessableEntity, "name is at most 80 characters")
		return
	case errors.Is(err, errMCPNameTaken):
		writeError(w, http.StatusUnprocessableEntity, "there is already an MCP token with this name")
		return
	case errors.Is(err, errMCPScopes):
		writeError(w, http.StatusUnprocessableEntity, "areas: give at least one area read or write; areas are events, lists, tasks and meals, levels none, read and write")
		return
	case err != nil:
		s.fail(w, "create mcp token", err)
		return
	}
	areas := map[string]auth.MCPLevel{}
	for area, level := range auth.MCPAccess(t.Scopes) {
		areas[area] = level
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": t.ID, "name": t.Name, "scopes": t.Scopes, "areas": areas, "token": value, "address": s.mcpAddress(r)})
}
