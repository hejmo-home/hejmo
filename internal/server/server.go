// Package server wires the box's HTTP handlers: web UI, setup wizard,
// sign-in and the REST API.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/passkey"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/places"
	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/update"
	"github.com/hejmo-home/hejmo/internal/weather"
	"github.com/hejmo-home/hejmo/web"
)

// Options configures the HTTP server.
type Options struct {
	// DB is the box's database (required).
	DB *store.DB
	// PKI holds the box's own certificate authority and certificate (required).
	PKI *pki.Store
	// HTTPSPort is the port clients reach HTTPS on, used in redirects and
	// links. Empty means 443.
	HTTPSPort string
	// Public is the public HTTPS certificate (ADR 0014). Nil means the
	// wizard offers only the box's own CA.
	Public PublicHTTPS
	// Sync syncs calendars from outside. Nil means a service with HTTP
	// fetching; main passes its own so the scheduler and the web UI share it.
	Sync *calsync.Service
	// Weather holds the weather of the home location, refreshed in the
	// background (ADR 0016). Nil means a service that never has a reading.
	Weather *weather.Service
	// LiveInterval is how often the box checks whether a connected display's
	// view changed (live updates, ticket 5.5). Zero means 2 seconds.
	LiveInterval time.Duration
	// Done, when closed, ends open live streams so a shutdown does not wait
	// for them. Nil means they end only when their client goes away.
	Done <-chan struct{}
	// CountTimeout is how long the box waits for a CalDAV server to count the
	// events of one calendar before the count shows as unknown. Zero means 10
	// seconds.
	CountTimeout time.Duration
	// UpdateDir is the data volume directory the host script of auto-update
	// shares with the box (ADR 0018). Empty means updates are not managed by a
	// host script: no settings file is written, no status is read and the
	// settings page does not offer auto-update.
	UpdateDir string
	// UpdateInstall says that the host script installs an uploaded update file
	// (ticket 221, HEJMO_HOST_UPDATER_INSTALL): the install script that is new
	// enough sets it. Without it the upload page says the script is too old.
	UpdateInstall bool
	// UpdateArch is the kind of box this is ("arm64" or "amd64"); empty means this build's.
	UpdateArch string
	// UpdateVerifier checks the signature of an uploaded update file. Nil means
	// the Sigstore public-good root that is part of the box; tests pass their own.
	UpdateVerifier update.Verifier
	// UpdateMaxSize is the biggest update file taken; zero means update.MaxFileSize.
	UpdateMaxSize int64
	// FreeSpace says how much room the data volume has; nil asks the file system. Tests pass their own.
	FreeSpace func(dir string) (uint64, error)
	// PhotoDir is the data volume directory for the photo screensaver (ticket
	// 178): the photo folder inside it and the shrunk copies the box keeps.
	// Empty means the box keeps no photos.
	PhotoDir string
	// Pictures downloads the web images of own articles (ticket 164). Nil means
	// a fetcher that only reaches public addresses; tests pass one that reaches their own server.
	Pictures *picture.Fetcher
	// Gate is the box's door to the outside (ticket 201). Nil means a gate
	// backed by DB, which is what the box uses.
	Gate *outbound.Gate
	// Resolver looks up the name of the box's LAN address in the router, to
	// prefill the router name of the HTTPS step (decision Q92). Nil means the
	// resolver of the home network (package outbound); tests pass a fake.
	Resolver NameResolver
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Logger receives errors. Nil means slog.Default().
	Logger *slog.Logger
	// Kiosk is the runner that sets the brightness of displays with a kiosk
	// app (ticket 179). The settings page reads its state and a touch on a
	// quiet display wakes it through it. Nil means displays are not driven:
	// no state is shown and a touch wakes nothing.
	Kiosk *KioskRunner
}

// PublicHTTPS is the box's publicly trusted certificate, set up through the
// wizard; implemented by publiccert.Manager.
type PublicHTTPS interface {
	// Ready reports whether a public setup is installed (its certificate may
	// still be missing, see HasCertificate).
	Ready() bool
	// HasCertificate reports whether the manager has a certificate to serve.
	// It is false while a stored setup is ready but its certificate could not
	// be loaded or obtained: the box then serves its own CA's certificate.
	HasCertificate() bool
	// Name is the public DNS name of the box.
	Name() string
	// Status is shown to parents.
	Status() publiccert.Status
	// Setup points the name to the box and obtains the certificate.
	Setup(context.Context, publiccert.Request) error
	// Clear forgets the public setup (back to the box's own CA).
	Clear() error
}

// NameResolver finds the names a DNS server knows for an address (a reverse
// lookup). *net.Resolver implements it.
type NameResolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

type server struct {
	resolver  NameResolver
	db        *store.DB
	pki       *pki.Store
	public    PublicHTTPS
	httpsPort string
	now       func() time.Time
	i18n      *i18n.Bundle
	pages     *template.Template
	logger    *slog.Logger
	limiter   *loginLimiter
	// passkeyTries counts refused passkey sign-ins per client address.
	passkeyTries *loginLimiter
	// ceremonies holds the passkey ceremonies that are begun and not finished.
	ceremonies *passkey.Ceremonies
	opens      *loginLimiter // pairing screens opened, per client address
	sync       *calsync.Service
	weather    *weather.Service
	gate       *outbound.Gate // every connection to the outside, and its switches
	places     *places.List   // the list of places on the box, loaded at start-up (ADR 0023)
	live       time.Duration
	countWait  time.Duration
	done       <-chan struct{}
	updateDir  string
	kiosk      *KioskRunner // nil when displays are not driven
	photos     *photoBox    // nil without a data directory for photos
	flashTexts flashTexts
	// pictures holds pictures made from an upload or a web image until their article is saved (ticket 164).
	pictures pendingPictures
	// fetcher downloads web images, only from public addresses.
	fetcher *picture.Fetcher
	// outlook holds the device sign-ins of Outlook that are begun and not finished (ticket 177).
	outlook outlookFlows
	rolled  sync.Map // household id -> the day its meal plan calendar was last rolled forward
	// What an uploaded update file needs (ticket 221).
	updateInstall  bool
	updateArch     string
	updateVerifier update.Verifier
	updateMax      int64
	freeSpace      func(string) (uint64, error)
	uploadMu       sync.Mutex // one upload, one yes and one settling at a time
}

// New returns the box's root HTTP handler.
func New(opts Options) http.Handler {
	dummyHash() // make it now, not during the first sign-in
	if opts.DB == nil {
		panic("server: Options.DB is required")
	}
	if opts.PKI == nil {
		panic("server: Options.PKI is required")
	}
	s := &server{db: opts.DB, pki: opts.PKI, public: opts.Public, httpsPort: opts.HTTPSPort, now: opts.Now, logger: opts.Logger, i18n: i18n.MustLoad(), places: places.Embedded()}
	if s.httpsPort == "" {
		s.httpsPort = "443"
	}
	if s.resolver = opts.Resolver; s.resolver == nil {
		s.resolver = outbound.HomeResolver{}
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.gate = opts.Gate; s.gate == nil {
		s.gate = outbound.New(s.db.Outbound(), s.db.Outbound())
	}
	if s.sync = opts.Sync; s.sync == nil {
		s.sync = calsync.New(s.db, nil, s.now)
		s.sync.SetGate(s.gate)
	}
	if s.weather = opts.Weather; s.weather == nil {
		s.weather = weather.New(weather.WithClock(s.now), weather.WithGate(s.gate))
	}
	if s.fetcher = opts.Pictures; s.fetcher == nil {
		s.fetcher = &picture.Fetcher{Gate: s.gate}
	}
	s.done = opts.Done
	s.kiosk = opts.Kiosk
	s.updateDir = opts.UpdateDir
	s.updateInstall, s.updateVerifier, s.updateMax, s.freeSpace = opts.UpdateInstall, opts.UpdateVerifier, opts.UpdateMaxSize, opts.FreeSpace
	if s.updateArch = opts.UpdateArch; s.updateArch == "" {
		s.updateArch = runtime.GOARCH
	}
	if opts.PhotoDir != "" {
		s.photos = newPhotoBox(opts.PhotoDir)
		if opts.Done != nil {
			go s.watchPhotoFolder(opts.Done)
		}
	}
	if s.live = opts.LiveInterval; s.live <= 0 {
		s.live = 2 * time.Second
	}
	if s.countWait = opts.CountTimeout; s.countWait <= 0 {
		s.countWait = 10 * time.Second
	}
	s.limiter = newLoginLimiter(5, 15*time.Minute, s.now)
	s.opens = newLoginLimiter(10, 10*time.Minute, s.now)
	s.passkeyTries = newLoginLimiter(10, 10*time.Minute, s.now)
	s.ceremonies = passkey.NewCeremonies(s.now)
	s.pages = parsePages()
	s.settleAtStart() // an installation that ended while the box was away, and what an upload left
	s.syncUpdateSettings()

	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /setup/https", s.handleHTTPSForm)
	mux.HandleFunc("POST /setup/https", s.handleHTTPSSubmit)
	mux.HandleFunc("POST /setup/https/public", s.handlePublicHTTPSSubmit)
	mux.HandleFunc("GET /setup/https/check", s.handleNameCheck)
	mux.HandleFunc("GET /setup/offline", s.handleOfflineQuestion)
	mux.HandleFunc("POST /setup/offline", s.handleOfflineAnswer)
	mux.HandleFunc("POST /setup/https/own-ca", s.handleSwitchToOwnCA)
	mux.HandleFunc("GET /ca", s.handleTrustPage)
	mux.HandleFunc("GET /ca/hejmo-ca.crt", s.handleCACert)
	mux.HandleFunc("GET /ca/hejmo.mobileconfig", s.handleMobileConfig)
	mux.HandleFunc("GET /setup", s.handleSetupForm)
	mux.HandleFunc("POST /setup", s.handleSetupSubmit)
	mux.HandleFunc("GET /setup/totp", s.handleSetupTOTPForm)
	mux.HandleFunc("POST /setup/totp", s.handleSetupTOTPSubmit)
	mux.HandleFunc("GET /setup/passkey", s.handlePasskeyOffer)
	mux.HandleFunc("GET /otp", s.handleOTPMoved)
	mux.HandleFunc("GET /account", s.handleAccountPage)
	mux.HandleFunc("POST /account/recovery-codes", s.handleRecoveryCodesNew)
	mux.HandleFunc("POST /account/recovery-codes/saved", s.handleRecoveryCodesSaved)
	mux.HandleFunc("GET /account/reset/{id}", s.handleResetPage)
	mux.HandleFunc("POST /account/reset/{id}", s.handleResetSubmit)
	mux.HandleFunc("POST /account/passkeys", s.handlePasskeyOfferForm)
	mux.HandleFunc("POST /account/passkeys/begin", s.handlePasskeyRegisterBegin)
	mux.HandleFunc("POST /account/passkeys/finish", s.handlePasskeyRegisterFinish)
	mux.HandleFunc("POST /account/passkeys/assert/begin", s.handlePasskeyAssertBegin)
	mux.HandleFunc("GET /account/passkeys/{id}", s.handlePasskeyPage)
	mux.HandleFunc("POST /account/passkeys/{id}", s.handlePasskeyRename)
	mux.HandleFunc("POST /account/passkeys/{id}/remove", s.handlePasskeyRemove)
	mux.HandleFunc("POST /login/passkey/begin", s.handleLoginPasskeyBegin)
	mux.HandleFunc("POST /login/passkey/finish", s.handleLoginPasskeyFinish)
	mux.HandleFunc("GET /sessions", s.handleSessionsPage)
	mux.HandleFunc("GET /sessions/{id}", s.handleSessionPage)
	mux.HandleFunc("POST /sessions/{id}/end", s.handleSessionEnd)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("POST /language", s.handleLanguageSwitch)
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("POST /first-steps/hide", s.handleFirstStepsHide)
	mux.HandleFunc("POST /first-steps/show", s.handleFirstStepsShow)
	mux.HandleFunc("POST /first-steps/skip", s.handleFirstStepSkip)
	mux.HandleFunc("POST /first-steps/undo", s.handleFirstStepUndo)
	mux.HandleFunc("GET /calendar", s.handleCalendarWeek)
	mux.HandleFunc("GET /calendar/search", s.handleCalendarSearch)
	mux.HandleFunc("GET /calendar/new", s.handleEventNew)
	mux.HandleFunc("POST /calendar/events", s.handleEventCreate)
	mux.HandleFunc("GET /calendar/events/{id}", s.handleEventPage)
	mux.HandleFunc("POST /calendar/events/{id}", s.handleEventEdit)
	mux.HandleFunc("POST /calendar/events/{id}/delete", s.handleEventDelete)
	mux.HandleFunc("GET /calendars", s.handleCalendarsPage)
	mux.HandleFunc("POST /calendars", s.handleCalendarAdd)
	mux.HandleFunc("GET /calendars/{id}", s.handleCalendarPage)
	mux.HandleFunc("POST /calendars/{id}/sync", s.handleCalendarSync)
	mux.HandleFunc("POST /calendars/{id}/delete", s.handleCalendarDelete)
	mux.HandleFunc("POST /calendars/{id}/read-only", s.handleCalendarReadOnly)
	mux.HandleFunc("GET /connections/new", s.handleConnectNew)
	mux.HandleFunc("POST /calendars/{id}/waste", s.handleCalendarWaste)
	mux.HandleFunc("POST /notes", s.handleNoteSave)
	mux.HandleFunc("GET /notes/{day}", s.handleNotePage)
	mux.HandleFunc("POST /notes/{day}", s.handleNoteChange)
	mux.HandleFunc("POST /notes/{day}/delete", s.handleNoteDelete)
	mux.HandleFunc("POST /connections", s.handleConnect)
	mux.HandleFunc("POST /connections/google/app", s.handleGoogleApp)
	mux.HandleFunc("GET "+googleSignInPath, s.handleGoogleSignIn)
	mux.HandleFunc("POST /connections/google/start", s.handleGoogleStart)
	mux.HandleFunc("GET "+googleCallbackPath, s.handleGoogleCallback)
	mux.HandleFunc("POST /connections/google/paste", s.handleGooglePaste)
	mux.HandleFunc("POST /connections/google/adopt", s.handleGoogleAdopt)
	mux.HandleFunc("POST /connections/outlook", s.handleOutlookStart)
	mux.HandleFunc("GET /connections/outlook/{flow}", s.handleOutlookFlow)
	mux.HandleFunc("POST /connections/outlook/{flow}/new", s.handleOutlookFlowNew)
	mux.HandleFunc("POST /connections/{id}/reconnect", s.handleOutlookReconnect)
	mux.HandleFunc("GET /connections/{id}", s.handleConnectionPage)
	mux.HandleFunc("POST /connections/{id}/calendars", s.handleConnectionCalendars)
	mux.HandleFunc("POST /connections/{id}/credentials", s.handleConnectionCredentials)
	mux.HandleFunc("POST /connections/{id}/delete", s.handleConnectionDelete)
	mux.HandleFunc("GET /caldav", s.handleCalDAVPage)
	mux.HandleFunc("POST /caldav/tokens", s.handleDeviceTokenCreate)
	mux.HandleFunc("POST /caldav/profile", s.handleIOSProfile)
	mux.HandleFunc("GET /caldav/tokens/{id}", s.handleDeviceTokenPage)
	mux.HandleFunc("POST /caldav/tokens/{id}/revoke", s.handleDeviceTokenRevoke)
	mux.HandleFunc("POST /caldav/calendars/{id}/share", s.handleCalendarShare)
	mux.HandleFunc("GET /people", s.handlePeoplePage)
	mux.HandleFunc("POST /people", s.handlePersonAdd)
	mux.HandleFunc("GET /people/{id}/invitation", s.handleInvitationPage)
	mux.HandleFunc("POST /people/{id}/invitation", s.handleInvitationCreate)
	mux.HandleFunc("POST /people/{id}/invitation/withdraw", s.handleInvitationWithdraw)
	mux.HandleFunc("GET /invite/{token}", s.handleInviteWelcome)
	mux.HandleFunc("GET /invite/{token}/account", s.handleInviteAccountForm)
	mux.HandleFunc("POST /invite/{token}/account", s.handleInviteAccountSubmit)
	mux.HandleFunc("GET /i/{token}", s.handleInviteShort)
	mux.HandleFunc("GET /people/{id}", s.handlePersonPage)
	mux.HandleFunc("POST /people/{id}", s.handlePersonSave)
	mux.HandleFunc("POST /people/{id}/move", s.handlePersonMove)
	mux.HandleFunc("GET /people/{id}/food", s.handleFoodPage)
	mux.HandleFunc("POST /people/{id}/food", s.handleFoodSave)
	mux.HandleFunc("GET /groups/new", s.handleGroupNew)
	mux.HandleFunc("POST /groups", s.handleGroupCreate)
	mux.HandleFunc("GET /groups/{id}", s.handleGroupPage)
	mux.HandleFunc("POST /groups/{id}", s.handleGroupSave)
	mux.HandleFunc("POST /groups/{id}/delete", s.handleGroupDelete)
	mux.HandleFunc("GET /tasks", s.handleTasksPage)
	mux.HandleFunc("POST /tasks", s.handleTaskAdd)
	mux.HandleFunc("POST /tasks/stars", s.handleStarBooking)
	mux.HandleFunc("GET /tasks/pauses/new", s.handlePauseNew)
	mux.HandleFunc("POST /tasks/pauses/new", s.handlePauseCreate)
	mux.HandleFunc("GET /tasks/pauses/{id}", s.handlePauseEditPage)
	mux.HandleFunc("POST /tasks/pauses/edit/{id}", s.handlePauseUpdate)
	mux.HandleFunc("POST /tasks/pauses/remove/{id}", s.handlePauseRemove)
	mux.HandleFunc("GET /tasks/{id}", s.handleTaskPage)
	mux.HandleFunc("POST /tasks/{id}/done", s.handleTaskDone)
	mux.HandleFunc("POST /tasks/{id}/subtasks", s.handleSubtaskAdd)
	mux.HandleFunc("POST /tasks/{id}/subtasks/{sub_id}", s.handleSubtaskSave)
	mux.HandleFunc("POST /tasks/{id}/subtasks/{sub_id}/done", s.handleSubtaskDone)
	mux.HandleFunc("POST /tasks/{id}/subtasks/{sub_id}/move", s.handleSubtaskMove)
	mux.HandleFunc("POST /tasks/{id}/subtasks/{sub_id}/delete", s.handleSubtaskDelete)
	mux.HandleFunc("POST /tasks/{id}/stars", s.handleTaskStars)
	mux.HandleFunc("POST /tasks/{id}/delete", s.handleTaskDelete)
	mux.HandleFunc("POST /tasks/{id}/routine", s.handleTaskLeaveRoutine)
	mux.HandleFunc("GET /routines/new", s.handleRoutineNewPage)
	mux.HandleFunc("POST /routines", s.handleRoutineCreate)
	mux.HandleFunc("GET /routines/{id}", s.handleRoutinePage)
	mux.HandleFunc("POST /routines/{id}/move", s.handleRoutineMove)
	mux.HandleFunc("POST /routines/{id}/tasks", s.handleRoutineAddTask)
	mux.HandleFunc("POST /routines/{id}/settings", s.handleRoutineSettings)
	mux.HandleFunc("POST /routines/{id}/delete", s.handleRoutineDissolve)
	mux.HandleFunc("POST /routines/{id}/template", s.handleRoutineSaveTemplate)
	mux.HandleFunc("GET /routines/templates", s.handleRoutineTemplates)
	mux.HandleFunc("GET /routines/templates/{id}", s.handleRoutineTemplatePage)
	mux.HandleFunc("POST /routines/templates/{id}/move", s.handleTemplateMove)
	mux.HandleFunc("POST /routines/templates/{id}/steps", s.handleTemplateAddStep)
	mux.HandleFunc("POST /routines/templates/{id}/settings", s.handleTemplateSettings)
	mux.HandleFunc("POST /routines/templates/{id}/delete", s.handleRoutineTemplateDelete)
	mux.HandleFunc("GET /routines/templates/{id}/steps/{step}", s.handleTemplateStepPage)
	mux.HandleFunc("POST /routines/templates/{id}/steps/{step}", s.handleTemplateStepSave)
	mux.HandleFunc("POST /routines/templates/{id}/steps/{step}/delete", s.handleTemplateStepDelete)
	mux.HandleFunc("GET /settings/lists", s.handleListsSettings)
	mux.HandleFunc("POST /settings/lists", s.handleListsSettingsSave)
	mux.HandleFunc("POST /settings/lists/units", s.handleUnitsSave)
	mux.HandleFunc("GET /settings/lists/always", s.handleAlways)
	mux.HandleFunc("POST /settings/lists/always", s.handleAlwaysSave)
	mux.HandleFunc("GET /settings/lists/always/plan", s.handleAlwaysPlan)
	mux.HandleFunc("GET /contacts", s.handleContacts)
	mux.HandleFunc("GET /contacts/new", s.handleContactNew)
	mux.HandleFunc("POST /contacts/new", s.handleContactCreate)
	mux.HandleFunc("GET /contacts/groups", s.handleContactGroups)
	mux.HandleFunc("POST /contacts/groups", s.handleContactGroupCreate)
	mux.HandleFunc("POST /contacts/groups/{id}/delete", s.handleContactGroupDelete)
	mux.HandleFunc("GET /contacts/{id}", s.handleContact)
	// "/contacts/{id}/edit" and "/contacts/groups/{id}" match a request alike ("/contacts/groups/edit"), which the mux
	// refuses as two patterns: one decides.
	mux.HandleFunc("GET /contacts/{first}/{second}", s.contactSubpage)
	mux.HandleFunc("POST /contacts/{first}/{second}", s.contactSubpage)
	mux.HandleFunc("GET /settings/lists/library", s.handleLibrary)
	mux.HandleFunc("GET /settings/lists/library/new", s.handleArticleNew)
	mux.HandleFunc("POST /settings/lists/library", s.handleArticleCreate)
	mux.HandleFunc("GET /settings/lists/library/suggest", s.handleArticleSuggest)
	mux.HandleFunc("GET /settings/lists/library/picture/{token}", s.handlePendingPicture)
	mux.HandleFunc("GET /settings/lists/library/plan/{id}", s.handleAutoPlan)
	mux.HandleFunc("GET /settings/lists/library/{id}", s.handleArticle)
	mux.HandleFunc("POST /settings/lists/library/{id}", s.handleArticleSave)
	mux.HandleFunc("POST /settings/lists/library/{id}/hidden", s.handleArticleHidden)
	mux.HandleFunc("POST /settings/lists/library/{id}/pantry", s.handleArticlePantry)
	mux.HandleFunc("POST /settings/lists/library/{id}/copy", s.handleArticleCopy)
	mux.HandleFunc("POST /settings/lists/library/{id}/delete", s.handleArticleDelete)
	mux.HandleFunc("GET /articles/{id}/picture", s.handleArticlePicture)
	mux.HandleFunc("GET /lists", s.handleListsPage)
	mux.HandleFunc("POST /lists", s.handleListCreate)
	mux.HandleFunc("GET /lists/{id}", s.handleListPage)
	mux.HandleFunc("POST /lists/{id}/settings", s.handleListSettings)
	mux.HandleFunc("POST /me/list-view", s.handleListView)
	mux.HandleFunc("POST /lists/{id}/delete", s.handleListDelete)
	mux.HandleFunc("POST /lists/{id}/entries", s.handleEntryAdd)
	mux.HandleFunc("GET /lists/{id}/suggest", s.handleListSuggest)
	mux.HandleFunc("GET /lists/{id}/quantity", s.handleListQuantity)
	mux.HandleFunc("POST /lists/{id}/entries/{entry}/quantity", s.handleEntryQuantity)
	mux.HandleFunc("POST /lists/{id}/entries/{entry}/done", s.handleEntryDone)
	mux.HandleFunc("POST /lists/{id}/entries/{entry}/delete", s.handleEntryDelete)
	mux.HandleFunc("POST /lists/{id}/entries/{entry}/move", s.handleEntryMove)
	mux.HandleFunc("POST /lists/{id}/completed/delete", s.handleTickedDelete)
	mux.HandleFunc("GET /meals", s.handleMealsPage)
	mux.HandleFunc("POST /meals", s.handleMealsSave)
	mux.HandleFunc("GET /meals/templates", s.handleTemplatesPage)
	mux.HandleFunc("GET /meals/templates/new", s.handleTemplateNew)
	mux.HandleFunc("POST /meals/templates", s.handleTemplateCreate)
	mux.HandleFunc("GET /meals/templates/{id}", s.handleTemplatePage)
	mux.HandleFunc("POST /meals/templates/{id}", s.handleTemplateSave)
	mux.HandleFunc("POST /meals/templates/{id}/delete", s.handleTemplateDelete)
	mux.HandleFunc("GET /mcp", s.handleMCPPage)
	mux.HandleFunc("POST /mcp", s.handleMCPPost)
	mux.HandleFunc("DELETE /mcp", s.handleMCPEndpoint)
	mux.HandleFunc("GET /mcp/tokens/new", s.handleMCPTokenNew)
	mux.HandleFunc("POST /mcp/tokens", s.handleMCPTokenCreate)
	mux.HandleFunc("GET /mcp/tokens/{id}", s.handleMCPTokenPage)
	mux.HandleFunc("POST /mcp/tokens/{id}/revoke", s.handleMCPTokenRevoke)
	mux.HandleFunc("GET /photos", s.handlePhotosPage)
	mux.HandleFunc("POST /photos/upload", s.handlePhotosUpload)
	mux.HandleFunc("POST /photos/albums", s.handleAlbumCreate)
	mux.HandleFunc("POST /photos/limit", s.handleLimitSave)
	mux.HandleFunc("GET /photos/albums/{id}", s.handleAlbumPage)
	mux.HandleFunc("POST /photos/albums/{id}/rename", s.handleAlbumRename)
	mux.HandleFunc("POST /photos/albums/{id}/delete", s.handleAlbumDelete)
	mux.HandleFunc("POST /photos/albums/{id}/selection", s.handleSelection)
	mux.HandleFunc("POST /photos/{id}/hidden", s.handlePhotoHidden)
	mux.HandleFunc("GET /display", s.handleDisplay)
	mux.HandleFunc("GET /display/state", s.handleDisplayState)
	mux.HandleFunc("GET /display/view", s.handleDisplayView)
	mux.HandleFunc("GET /display/events", s.handleDisplayEvents)
	mux.HandleFunc("POST /display/wake", s.handleDisplayWake)
	mux.HandleFunc("GET /display/saver", s.handleDisplaySaver)
	mux.HandleFunc("GET /display/saver/photos", s.serveDisplaySaver)
	mux.HandleFunc("GET /display/photo/{id}", s.serveDisplayPhoto)
	mux.HandleFunc("GET /display/person/{id}", s.handleDisplayPerson)
	mux.HandleFunc("GET /display/person/{id}/view", s.handleDisplayPersonView)
	mux.HandleFunc("POST /display/tasks/{id}/done", s.handleDisplayTaskDone)
	mux.HandleFunc("POST /display/tasks/{id}/subtasks/{sub_id}/done", s.handleDisplaySubtaskDone)
	mux.HandleFunc("GET /display/lists", s.handleDisplayLists)
	mux.HandleFunc("GET /display/lists/{id}", s.handleDisplayList)
	mux.HandleFunc("GET /display/lists/{id}/view", s.handleDisplayListView)
	mux.HandleFunc("POST /display/lists/{id}/entries", s.handleDisplayEntryAdd)
	mux.HandleFunc("GET /display/lists/{id}/suggest", s.handleDisplayListSuggest)
	mux.HandleFunc("GET /display/lists/{id}/quantity", s.handleDisplayListQuantity)
	mux.HandleFunc("POST /display/lists/{id}/entries/{entry}/quantity", s.handleDisplayEntryQuantity)
	mux.HandleFunc("POST /display/list-view", s.handleDisplayListViewChoice)
	mux.HandleFunc("GET /display/articles/{id}/picture", s.handleDisplayArticlePicture)
	mux.HandleFunc("GET /display/rewards/{id}/picture", s.handleDisplayRewardPicture)
	mux.HandleFunc("GET /display/contacts/{id}/photo", s.handleDisplayContactPhoto)
	mux.HandleFunc("POST /display/lists/{id}/entries/{entry}/done", s.handleDisplayEntryDone)
	mux.HandleFunc("POST /display/lists/{id}/completed/delete", s.handleDisplayTickedDelete)
	mux.HandleFunc("GET /displays", s.handleDisplaysPage)
	mux.HandleFunc("POST /displays", s.handleDisplayPair)
	mux.HandleFunc("GET /displays/{id}", s.handleDisplayPage)
	mux.HandleFunc("POST /displays/{id}/unpair", s.handleDisplayUnpair)
	mux.HandleFunc("GET /settings", s.handleSettingsHub)
	mux.HandleFunc("GET /places", s.handlePlaces)
	mux.HandleFunc("GET /settings/licences", s.handleLicences)
	mux.HandleFunc("GET /settings/household", s.handleSettingsPage)
	mux.HandleFunc("POST /settings/household", s.handleSettingsSave)
	mux.HandleFunc("GET /settings/connections", s.handleConnectionsPage)
	mux.HandleFunc("POST /settings/connections", s.handleConnectionsSave)
	mux.HandleFunc("GET /settings/connections/offline", s.handleOfflineOnPage)
	mux.HandleFunc("POST /settings/connections/offline", s.handleOfflineOn)
	mux.HandleFunc("GET /settings/connections/online", s.handleOnlinePage)
	mux.HandleFunc("POST /settings/connections/online", s.handleOnline)
	mux.HandleFunc("GET /settings/connections/update", s.handleUpdatePage)
	mux.HandleFunc("POST /settings/connections/update", s.handleUpdateUpload)
	mux.HandleFunc("GET /settings/connections/update/install", s.handleInstallPage)
	mux.HandleFunc("POST /settings/connections/update/install", s.handleInstall)
	mux.HandleFunc("POST /settings/connections/update/discard", s.handleDiscard)
	mux.HandleFunc("GET /settings/https", s.handleHTTPSSettings)
	mux.HandleFunc("POST /settings/https", s.handleHTTPSSettingsSave)
	mux.HandleFunc("GET /personal-devices", s.handlePersonalDevicesPage)
	mux.HandleFunc("GET /personal-devices/{id}", s.handlePersonalDevicePage)
	mux.HandleFunc("POST /personal-devices/pairings", s.handlePersonalDevicePairingCreate)
	mux.HandleFunc("POST /personal-devices/pairings/{id}/confirm", s.handlePersonalDevicePairingConfirm)
	mux.HandleFunc("POST /personal-devices/{id}/revoke", s.handlePersonalDeviceRevoke)
	mux.HandleFunc("POST /personal-devices/{id}/repair", s.handlePersonalDeviceRepair)
	mux.HandleFunc("GET /personal-devices/join/{code}", s.handlePersonalDeviceJoin)
	mux.HandleFunc("GET /manifest.webmanifest", s.handleManifest)
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker)
	mux.HandleFunc("POST /update/dismiss", s.handleUpdateDismiss)
	mux.HandleFunc("POST /displays/{id}/settings", s.handleDisplaySettings)
	mux.HandleFunc("POST /displays/{id}/layout/copy", s.handleDisplayLayoutCopy)
	s.routeAPI(mux)
	s.routeCalDAV(mux)
	return s.requireHTTPS(sameOrigin(withPageLanguage(withRewardPages(mux, s.routeRewards()))))
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	status, code := "ok", http.StatusOK
	if err := s.db.SQL().PingContext(r.Context()); err != nil {
		status, code = "database unavailable", http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func (s *server) handleHome(w http.ResponseWriter, r *http.Request) {
	if !s.db.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	p, ok := s.currentPerson(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	s.renderHome(w, r, http.StatusOK, p, h, nil)
}

// parsePages returns the embedded templates, parsed once per process: every
// server shares the set and only copies it (pagesFor), never executes it, and
// parsing it per server.New cost tens of MB per test box (ticket 238). "t" is
// replaced per language.
var parsePages = sync.OnceValue(parseEmbeddedPages)

func parseEmbeddedPages() *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"t":      func(string) string { return "" },
		"tf":     func(string, ...string) string { return "" },
		"tfh":    func(string, ...string) template.HTML { return "" },
		"plural": func(string, int) template.HTML { return "" },
		"dict":   dict,
		"list":   func(v ...any) []any { return v },
		"has":    func(list []string, v string) bool { return slices.Contains(list, v) },
		"errIn":  errIn,
		"errKey": errKey,
		"strs":   strs,
		"rich":   rich,
		"host":   withHost,
		"repl":   repl,
		"plain":  plainText,
		"acct":   accountText,
	}).ParseFS(web.Templates, "templates/*.html"))
}

func (s *server) render(w http.ResponseWriter, r *http.Request, status int, name string, data map[string]any) {
	var lang string
	if strings.HasPrefix(name, "display_") {
		lang = s.displayLang(r) // a parent's choice does not reach displays
	} else {
		lang = s.pageLanguage(r)
	}
	if data == nil {
		data = map[string]any{}
	}
	data["Lang"] = lang
	for _, k := range []string{"Error", "ErrorField", "FieldError", "Flash"} { // templates compare them
		if _, ok := data[k]; !ok {
			data[k] = ""
		}
	}
	if !strings.HasPrefix(name, "display_") {
		fr := s.frameFor(r)
		fr.setLanguage(lang, r)
		fr.PlainBrand = plainBrandPages[name]
		if href, _ := data["BackHref"].(string); href != "" { // a page that leads back to a named page
			text, _ := data["BackText"].(string)
			fr.Back, fr.BackHref, fr.BackLabel, fr.BackText = false, href, "", text
		}
		if parts := crumbsFor(name, data); len(parts) > 0 && data["Wizard"] == nil { // a trail replaces the back link (ticket 182); a wizard is a linear flow, not below Settings
			for _, c := range parts {
				label := c.Name
				if c.Key != "" {
					label = s.i18n.T(lang, c.Key)
				}
				fr.Crumbs = append(fr.Crumbs, crumbLink{c.Href, label})
			}
			fr.Back, fr.BackHref, fr.BackLabel, fr.BackText = false, "", "", ""
		}
		key, arg := s.takeFlash(w, r) // always consumed, so it cannot show later
		switch own, _ := data["SuccessKey"].(string); {
		case key == "flash.lang.error":
			fr.Error = s.i18n.T(lang, key)
		case key != "" && status >= http.StatusBadRequest:
			// A failed form shows no success line, not even one left over from before.
		case data["SuccessText"] != nil:
			text, _ := data["SuccessText"].(string)
			fr.Success = template.HTML(html.EscapeString(text)) // a page that shows the result itself, and names the thing
		case data["SuccessMsg"] != nil:
			msg, _ := data["SuccessMsg"].(i18n.Message)
			fr.Success = template.HTML(html.EscapeString(s.i18n.Format(lang, msg))) // the same, with values filled in
		case own != "":
			fr.Success = template.HTML(html.EscapeString(s.i18n.T(lang, own))) // a page that shows the result itself
		case key != "":
			fr.Success = s.successHTML(lang, key, arg)
			data["Flash"] = key
		}
		if inline, _ := data["SuccessInline"].(bool); inline {
			data["Success"], fr.Success = fr.Success, "" // the page places the line itself
		}
		data["Frame"] = fr
	}
	var buf bytes.Buffer
	err := s.execute(&buf, name, lang, data)
	if err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Language", lang)
		w.WriteHeader(status)
		_, _ = buf.WriteTo(w)
	} else {
		s.fail(w, "render "+name, err)
	}
}

// execute runs the named page template in the given language.
func (s *server) execute(w io.Writer, name, lang string, data any) error {
	tpl, err := s.pagesFor(lang)
	if err != nil {
		return err
	}
	return tpl.ExecuteTemplate(w, name, data)
}

// pagesFor returns the template set whose text functions speak lang. Copying
// the whole set per render cost several MB a page, and a copy per server another
// tens of MB per test box (ticket 238); a set is copied once per bundle and
// language for the whole process and then only executed, which html/template
// allows from many goroutines at once. A bundle is loaded once (i18n.MustLoad),
// so the cache stays small; past maxCachedSets an unknown pair is copied for that
// render alone.
func (s *server) pagesFor(lang string) (*template.Template, error) {
	key := pageSetKey{s.pages, s.i18n, lang}
	pageSets.RLock()
	tpl, ok := pageSets.byKey[key]
	pageSets.RUnlock()
	if ok {
		return tpl, nil
	}
	// Copy outside the lock: two renders may both copy on a first miss; the
	// loser of the race discards its copy and returns the cached one (see below).
	tpl, err := s.pages.Clone()
	if err != nil {
		return nil, err
	}
	bundle := s.i18n
	tpl = tpl.Funcs(template.FuncMap{
		"t":      func(k string) string { return bundle.T(lang, k) },
		"tf":     func(k string, kv ...string) string { return bundle.Format(lang, i18n.Msg(k, kv...)) },
		"tfh":    func(k string, kv ...string) template.HTML { return formatHTML(bundle, lang, k, kv...) },
		"plural": func(k string, n int) template.HTML { return pluralHTML(bundle, lang, k, n) },
	})
	pageSets.Lock()
	defer pageSets.Unlock()
	if cached, ok := pageSets.byKey[key]; ok {
		return cached, nil
	}
	if len(pageSets.byKey) < maxCachedSets {
		if pageSets.byKey == nil {
			pageSets.byKey = map[pageSetKey]*template.Template{}
		}
		pageSets.byKey[key] = tpl
	}
	return tpl, nil
}

type pageSetKey struct {
	pages  *template.Template // the source set, so servers with other pages never share a copy
	bundle *i18n.Bundle
	lang   string
}

var pageSets struct {
	sync.RWMutex
	byKey map[pageSetKey]*template.Template
}

const maxCachedSets = 16

func (s *server) fail(w http.ResponseWriter, what string, err error) {
	s.logger.Error(what, "err", err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// repl fills the {name} placeholders of a translated text: repl(text, "{n}", "3", ...).
func repl(text string, pairs ...string) string { return strings.NewReplacer(pairs...).Replace(text) }

// errIn reports whether a page names the field in its ErrorFields (field to
// text key), for pages whose error belongs to several fields at once.
func errIn(root any, field string) bool {
	_, ok := errKey0(root)[field]
	return ok
}

// errKey is the text key ErrorFields gives the field, or "".
func errKey(root any, field string) string { return errKey0(root)[field] }

func errKey0(root any) map[string]string {
	if m, ok := root.(map[string]any); ok {
		if f, ok := m["ErrorFields"].(map[string]string); ok {
			return f
		}
	}
	return nil
}

// dict builds a map for passing several values to a sub-template.
func dict(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			m[k] = kv[i+1]
		}
	}
	return m
}

// parentUpdateNotice is the update notice for a signed-in parent; children see none.
func (s *server) parentUpdateNotice(p store.Person, h store.Household) *updateView {
	if p.Role != store.RoleParent {
		return nil
	}
	return s.updateNotice(h)
}

// strs turns a form value (nothing, one string or a list) into a list for the
// chips and options that mark their selected values.
func strs(v any) []string {
	switch x := v.(type) {
	case string:
		if x != "" {
			return []string{x}
		}
	case []string:
		return x
	}
	return nil
}
