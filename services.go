// Platform services: the standard connections the platform gives an
// application when the customer switches them on in My Apps. The page shows
// whether each one is connected and lets a visitor test it.
//
// Keys and passwords stay on the server. The page only ever sees the names of
// the settings, the address of the service and the result of a test.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const notConnected = "Not connected. Switch it on in My Apps, under Platform services."

// service is one platform connection as the page shows it.
type service struct {
	Connected bool     `json:"connected"`
	Address   string   `json:"address,omitempty"`
	Settings  []string `json:"settings"`
	Missing   []string `json:"missing,omitempty"`
	Message   string   `json:"message,omitempty"`
}

type services struct {
	ModelHub service `json:"modelHub"`
	Database service `json:"database"`
	Storage  service `json:"storage"`
}

var modelHubSettings = []string{"GLBNXT_MODELHUB_BASE_URL", "GLBNXT_MODELHUB_API_KEY"}

var databaseSettings = []string{
	"GLBNXT_DATABASE_HOST", "GLBNXT_DATABASE_PORT", "GLBNXT_DATABASE_NAME",
	"GLBNXT_DATABASE_USER", "GLBNXT_DATABASE_PASSWORD",
}

var storageSettings = []string{
	"GLBNXT_S3_ENDPOINT", "GLBNXT_S3_BUCKET", "GLBNXT_S3_ACCESS_KEY",
	"GLBNXT_S3_SECRET_KEY", "GLBNXT_S3_REGION",
}

// lookup returns the values of names, and the names that are not set.
func lookup(names []string) (map[string]string, []string) {
	values := map[string]string{}
	var missing []string
	for _, name := range names {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			missing = append(missing, name)
			continue
		}
		values[name] = v
	}
	return values, missing
}

func readServices() services {
	var s services

	hub, missing := lookup(modelHubSettings)
	s.ModelHub = service{Settings: modelHubSettings, Missing: missing, Connected: len(missing) == 0}
	if s.ModelHub.Connected {
		if u, err := url.Parse(hub["GLBNXT_MODELHUB_BASE_URL"]); err == nil && u.Host != "" {
			s.ModelHub.Address = u.Scheme + "://" + u.Host
		}
	} else {
		s.ModelHub.Message = notConnected
	}

	db, missing := lookup(databaseSettings)
	s.Database = service{Settings: databaseSettings, Missing: missing, Connected: len(missing) == 0}
	if s.Database.Connected {
		s.Database.Address = db["GLBNXT_DATABASE_NAME"] + " on " + db["GLBNXT_DATABASE_HOST"]
	} else {
		s.Database.Message = notConnected
	}

	st, missing := lookup(storageSettings)
	s.Storage = service{Settings: storageSettings, Missing: missing, Connected: len(missing) == 0}
	if s.Storage.Connected {
		s.Storage.Address = st["GLBNXT_S3_BUCKET"]
		if u, err := url.Parse(st["GLBNXT_S3_ENDPOINT"]); err == nil && u.Host != "" {
			s.Storage.Address += " on " + u.Host
		}
	} else {
		s.Storage.Message = notConnected
	}
	return s
}

// testResult is what a test button shows.
type testResult struct {
	OK      bool     `json:"ok"`
	Message string   `json:"message"`
	Models  []string `json:"models,omitempty"`
	Total   int      `json:"total,omitempty"`
	Visits  []visit  `json:"visits,omitempty"`
	Files   []file   `json:"files,omitempty"`
}

type file struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	StoredAt time.Time `json:"storedAt"`
}

type visit struct {
	ID        int64     `json:"id"`
	VisitedAt time.Time `json:"visitedAt"`
	Version   string    `json:"version"`
}

// handleTest wraps a test: one shared rate limit (the page is public) and a
// time limit, so a hanging service never holds a request for long.
func handleTest(logger *slog.Logger, limit *limiter, run func(context.Context) (testResult, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !limit.allow() {
			writeJSON(w, http.StatusTooManyRequests, testResult{Message: "That is a lot of tests. Wait a few seconds and try again."})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result, err := run(ctx)
		if err != nil {
			// The detail goes to the log, where the customer looks next; the
			// page gets a plain sentence without addresses or names.
			logger.Warn("Platform service test failed", "path", r.URL.Path, "error", err.Error())
			writeJSON(w, http.StatusOK, testResult{Message: pageMessage(err)})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// failure is an error with a sentence for the page and a detail for the log.
type failure struct {
	sentence string
	detail   error
}

func (f failure) Error() string {
	if f.detail == nil {
		return f.sentence
	}
	return f.sentence + " (" + f.detail.Error() + ")"
}

func (f failure) Unwrap() error { return f.detail }

// pageMessage is the part of an error the page may show.
func pageMessage(err error) string {
	var f failure
	if errors.As(err, &f) {
		return f.sentence
	}
	return "The test did not work. The logs of this application say why."
}

var modelHubClient = &http.Client{Timeout: 10 * time.Second}

// testModelHub asks the Model Hub which models this application may use.
// The Model Hub speaks the OpenAI API, so this is GET <base URL>/models.
func testModelHub(ctx context.Context) (testResult, error) {
	values, missing := lookup(modelHubSettings)
	if len(missing) > 0 {
		return testResult{Message: notConnected}, nil
	}

	endpoint := strings.TrimRight(values["GLBNXT_MODELHUB_BASE_URL"], "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return testResult{}, failure{"The Model Hub address in GLBNXT_MODELHUB_BASE_URL is not a valid address.", err}
	}
	req.Header.Set("Authorization", "Bearer "+values["GLBNXT_MODELHUB_API_KEY"])
	req.Header.Set("Accept", "application/json")

	resp, err := modelHubClient.Do(req)
	if err != nil {
		return testResult{}, failure{"We could not reach the Model Hub.", err}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return testResult{}, failure{"The Model Hub did not accept this application's key. Switch Model Hub off and on again in My Apps for a new key.", fmt.Errorf("status %d", resp.StatusCode)}
	case resp.StatusCode != http.StatusOK:
		return testResult{}, failure{"The Model Hub answered with an error. Try again in a moment.", fmt.Errorf("status %d", resp.StatusCode)}
	}

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&body); err != nil {
		return testResult{}, failure{"The Model Hub sent an answer we could not read.", err}
	}

	models := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if id := clean(m.ID, 120); id != "" {
			models = append(models, id)
		}
	}
	sort.Strings(models)
	total := len(models)
	if len(models) > 20 {
		models = models[:20]
	}

	message := fmt.Sprintf("Connected. This application may use %d models.", total)
	switch total {
	case 0:
		message = "Connected, but no models are available to this application yet."
	case 1:
		message = "Connected. This application may use 1 model."
	}
	return testResult{OK: true, Message: message, Models: models, Total: total}, nil
}

// databaseConfig builds the connection from the five settings. The server
// expects TLS; the platform gives no certificate to check it against, so the
// connection is encrypted without checking whose certificate it is.
func databaseConfig() (*pgx.ConnConfig, bool, error) {
	values, missing := lookup(databaseSettings)
	if len(missing) > 0 {
		return nil, false, nil
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(values["GLBNXT_DATABASE_USER"], values["GLBNXT_DATABASE_PASSWORD"]),
		Host:     net.JoinHostPort(values["GLBNXT_DATABASE_HOST"], values["GLBNXT_DATABASE_PORT"]),
		Path:     "/" + values["GLBNXT_DATABASE_NAME"],
		RawQuery: url.Values{"sslmode": {"require"}, "connect_timeout": {"10"}, "application_name": {"my-first-app"}}.Encode(),
	}
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, true, failure{"The database settings are not valid. Check GLBNXT_DATABASE_PORT.", nil}
	}
	return cfg, true, nil
}

// testDatabase writes one visit to the application's own table and reads the
// latest five back: a write and a read, the two things an application needs.
func testDatabase(ctx context.Context) (testResult, error) {
	cfg, ok, err := databaseConfig()
	if !ok {
		return testResult{Message: notConnected}, nil
	}
	if err != nil {
		return testResult{}, err
	}

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return testResult{}, databaseFailure(err)
	}
	defer conn.Close(context.Background())

	// The application owns its database, so it creates its own table.
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS visits (
		id         bigserial PRIMARY KEY,
		visited_at timestamptz NOT NULL DEFAULT now(),
		version    text NOT NULL
	)`); err != nil {
		return testResult{}, databaseFailure(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO visits (version) VALUES ($1)`, version); err != nil {
		return testResult{}, databaseFailure(err)
	}

	var total int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM visits`).Scan(&total); err != nil {
		return testResult{}, databaseFailure(err)
	}
	rows, err := conn.Query(ctx, `SELECT id, visited_at, version FROM visits ORDER BY id DESC LIMIT 5`)
	if err != nil {
		return testResult{}, databaseFailure(err)
	}
	visits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (visit, error) {
		var v visit
		err := row.Scan(&v.ID, &v.VisitedAt, &v.Version)
		return v, err
	})
	if err != nil {
		return testResult{}, databaseFailure(err)
	}

	message := fmt.Sprintf("Connected. We wrote a visit to the table visits, which now holds %d.", total)
	if total == 1 {
		message = "Connected. We created the table visits and wrote the first visit to it."
	}
	return testResult{OK: true, Message: message, Visits: visits, Total: total}, nil
}

// databaseFailure turns a database error into a sentence a customer can act on.
func databaseFailure(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01", "28000":
			return failure{"The database did not accept this application's login. Switch Database off and on again in My Apps.", err}
		case "3D000":
			return failure{"The database in GLBNXT_DATABASE_NAME does not exist.", err}
		case "42501":
			return failure{"This application's login may not do this in the database.", err}
		}
		return failure{"The database answered with an error.", err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failure{"The database did not answer in time.", err}
	}
	return failure{"We could not reach the database.", err}
}

// storagePrefix keeps the test's files apart from anything else in the bucket.
const storagePrefix = "my-first-app/"

// storageClient builds an S3 client from the five settings. The endpoint is a
// full address such as https://s3-api.example.com; any S3 library works the same way.
func storageClient() (*minio.Client, string, bool, error) {
	values, missing := lookup(storageSettings)
	if len(missing) > 0 {
		return nil, "", false, nil
	}
	u, err := url.Parse(values["GLBNXT_S3_ENDPOINT"])
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, "", true, failure{"The storage address in GLBNXT_S3_ENDPOINT is not a valid address.", err}
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(values["GLBNXT_S3_ACCESS_KEY"], values["GLBNXT_S3_SECRET_KEY"], ""),
		Secure: u.Scheme == "https",
		Region: values["GLBNXT_S3_REGION"],
	})
	if err != nil {
		return nil, "", true, failure{"The storage settings are not valid.", err}
	}
	return client, values["GLBNXT_S3_BUCKET"], true, nil
}

// testStorage stores one small file in the application's own bucket, lists
// the files it stored before and reads the new one back: a write, a list and
// a read, the three things an application needs.
func testStorage(ctx context.Context) (testResult, error) {
	client, bucket, ok, err := storageClient()
	if !ok {
		return testResult{Message: notConnected}, nil
	}
	if err != nil {
		return testResult{}, err
	}

	now := time.Now().UTC()
	name := storagePrefix + "visit-" + now.Format("20060102-150405.000000000") + ".txt"
	content := "A visit at " + now.Format(time.RFC3339) + " from version " + version + ".\n"
	if _, err := client.PutObject(ctx, bucket, name, strings.NewReader(content), int64(len(content)),
		minio.PutObjectOptions{ContentType: "text/plain; charset=utf-8"}); err != nil {
		return testResult{}, storageFailure(err)
	}

	obj, err := client.GetObject(ctx, bucket, name, minio.GetObjectOptions{})
	if err != nil {
		return testResult{}, storageFailure(err)
	}
	defer obj.Close()
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, io.LimitReader(obj, 1<<16)); err != nil {
		return testResult{}, storageFailure(err)
	}
	if buf.String() != content {
		return testResult{}, failure{"The file we read back is not the file we stored.", nil}
	}

	var files []file
	for o := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: storagePrefix}) {
		if o.Err != nil {
			return testResult{}, storageFailure(o.Err)
		}
		files = append(files, file{Name: strings.TrimPrefix(o.Key, storagePrefix), Size: o.Size, StoredAt: o.LastModified})
	}
	total := len(files)
	sort.Slice(files, func(i, j int) bool { return files[i].Name > files[j].Name })
	if len(files) > 5 {
		files = files[:5]
	}

	message := fmt.Sprintf("Connected. We stored a file in your bucket and read it back; it now holds %d files from this test.", total)
	if total == 1 {
		message = "Connected. We stored the first file in your bucket and read it back."
	}
	return testResult{OK: true, Message: message, Files: files, Total: total}, nil
}

// storageFailure turns a storage error into a sentence a customer can act on.
func storageFailure(err error) error {
	switch minio.ToErrorResponse(err).Code {
	case "InvalidAccessKeyId", "SignatureDoesNotMatch":
		return failure{"The storage did not accept this application's login. Switch Storage off and on again in My Apps.", err}
	case "AccessDenied":
		return failure{"This application's login may not do this in the bucket.", err}
	case "NoSuchBucket":
		return failure{"The bucket in GLBNXT_S3_BUCKET does not exist.", err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failure{"The storage did not answer in time.", err}
	}
	return failure{"We could not reach the storage.", err}
}
