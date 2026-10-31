package api

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"flock/internal/capture"
	"flock/internal/dumps"
	"flock/internal/laravellog"
	"flock/internal/mail"
)

func mailDir(b Backend) string { return filepath.Join(b.Root(), "mail") }

func registerMail(mux *http.ServeMux, b Backend) {
	mux.HandleFunc("GET /v1/mail", func(w http.ResponseWriter, _ *http.Request) {
		messages, err := mail.List(mailDir(b))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if messages == nil {
			messages = []mail.Message{}
		}
		writeJSON(w, http.StatusOK, messages)
	})

	mux.HandleFunc("DELETE /v1/mail", func(w http.ResponseWriter, r *http.Request) {
		if err := mail.Clear(mailDir(b), r.URL.Query().Get("inbox")); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /v1/mail/{id}", func(w http.ResponseWriter, r *http.Request) {
		msg, ok := findMessage(w, b, r.PathValue("id"))
		if !ok {
			return
		}
		detail, err := mail.Parse(msg)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	})

	mux.HandleFunc("GET /v1/mail/{id}/raw", func(w http.ResponseWriter, r *http.Request) {
		msg, ok := findMessage(w, b, r.PathValue("id"))
		if !ok {
			return
		}
		w.Header().Set("Content-Type", "message/rfc822")
		http.ServeFile(w, r, msg.Path)
	})

	mux.HandleFunc("GET /v1/mail/{id}/attachments/{index}", func(w http.ResponseWriter, r *http.Request) {
		msg, ok := findMessage(w, b, r.PathValue("id"))
		if !ok {
			return
		}
		index, err := strconv.Atoi(r.PathValue("index"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		att, data, err := mail.AttachmentData(msg, index)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		w.Header().Set("Content-Type", att.ContentType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": att.Filename}))
		_, _ = w.Write(data)
	})
}

func findMessage(w http.ResponseWriter, b Backend, id string) (mail.Message, bool) {
	msg, err := mail.Find(mailDir(b), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return mail.Message{}, false
	}
	return msg, true
}

func registerDumps(mux *http.ServeMux, b Backend) {
	mux.HandleFunc("GET /v1/dumps", func(w http.ResponseWriter, _ *http.Request) {
		entries, err := dumps.List(b.Root())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		sites := b.Status().Sites
		for i := range entries {
			entries[i].Site = siteFor(sites, entries[i].Source, entries[i].Request)
		}
		writeJSON(w, http.StatusOK, entries)
	})

	mux.HandleFunc("GET /v1/dumps/header", func(w http.ResponseWriter, _ *http.Request) {
		header, err := dumps.Header(b.Root())
		if err != nil {
			writeError(w, http.StatusNotFound, errors.New("the dump server has not run yet"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(header))
	})

	mux.HandleFunc("DELETE /v1/dumps", func(w http.ResponseWriter, _ *http.Request) {
		if err := dumps.Clear(b.Root()); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// registerEvents serves what the agent captured besides dumps (see package capture).
func registerEvents(mux *http.ServeMux, b Backend) {
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		var kinds []string
		if k := r.URL.Query().Get("kind"); k != "" {
			kinds = strings.Split(k, ",")
			if err := capture.Valid(kinds); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		events, err := capture.List(b.Root(), limit, kinds...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		sites := b.Status().Sites
		for i := range events {
			events[i].Site = siteFor(sites, events[i].Source, events[i].Request)
		}
		writeJSON(w, http.StatusOK, events)
	})

	mux.HandleFunc("DELETE /v1/events", func(w http.ResponseWriter, _ *http.Request) {
		if err := capture.Clear(b.Root()); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// siteFor attributes a dump or event to a site by the file that caused it, or by the request host.
func siteFor(sites []SiteStatus, source *dumps.Source, request *dumps.Request) string {
	if source != nil {
		for _, s := range sites {
			if inDir(source.File, s.Path) {
				return s.Domain
			}
		}
	}
	if request != nil {
		if u, err := url.Parse(request.URI); err == nil {
			// The longest name wins, as in routing: accounts.shop.test can be another site's alias.
			host, best, site := u.Hostname(), "", ""
			for _, s := range sites {
				names := []string{s.Domain}
				for _, d := range s.Domains {
					if d.Alias && d.Problem == "" {
						names = append(names, d.Host)
					}
				}
				for _, name := range names {
					if (host == name || strings.HasSuffix(host, "."+name)) && len(name) > len(best) {
						best, site = name, s.Domain
					}
				}
			}
			return site
		}
	}
	return ""
}

func registerLogs(mux *http.ServeMux, b Backend) {
	logDir := func() string { return filepath.Join(b.Root(), "log") }

	mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, _ *http.Request) {
		services := []string{}
		if entries, err := os.ReadDir(logDir()); err == nil {
			for _, e := range entries {
				if name, ok := strings.CutSuffix(e.Name(), ".log"); ok {
					services = append(services, name)
				}
			}
		}

		sites := []string{}
		for _, s := range b.Status().Sites {
			if len(laravellog.Files(s.Path)) > 0 {
				sites = append(sites, s.Name)
			}
		}
		writeJSON(w, http.StatusOK, map[string][]string{"services": services, "sites": sites})
	})

	mux.HandleFunc("GET /v1/logs/services/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid log name %q", name))
			return
		}
		lines := 300
		if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n > 0 {
			lines = n
		}
		text, err := tailLines(filepath.Join(logDir(), name+".log"), lines)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(text))
	})

	mux.HandleFunc("GET /v1/logs/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		site, ok := findSite(w, b, r.PathValue("name"))
		if !ok {
			return
		}
		limit := 200
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
			limit = n
		}
		entries, err := laravellog.Read(site.Path, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, entries)
	})

	mux.HandleFunc("DELETE /v1/logs/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		site, ok := findSite(w, b, r.PathValue("name"))
		if !ok {
			return
		}
		if err := laravellog.Clear(site.Path); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func findSite(w http.ResponseWriter, b Backend, name string) (SiteStatus, bool) {
	for _, s := range b.Status().Sites {
		if s.Name == name {
			return s, true
		}
	}
	writeError(w, http.StatusNotFound, errors.New("no site named "+name))
	return SiteStatus{}, false
}

// tailLines returns the last n lines of a file (reading at most the final 1 MiB).
func tailLines(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	const window = 1 << 20
	offset := max(info.Size()-window, 0)
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return "", err
	}

	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:] // first line is probably cut in half
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// inDir reports whether file lies inside dir. On Windows either slash may separate the parts
// (PHP reports both) and case doesn't matter (C:\ and c:\ are the same drive).
func inDir(file, dir string) bool {
	f, d := filepath.ToSlash(file), strings.TrimSuffix(filepath.ToSlash(dir), "/")+"/"
	if runtime.GOOS == "windows" {
		return len(f) > len(d) && strings.EqualFold(f[:len(d)], d)
	}
	return strings.HasPrefix(f, d)
}
