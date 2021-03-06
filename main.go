package main

import (
	"context"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/thrgamon/go-utils/env"
	urepo "github.com/thrgamon/go-utils/repo/user"
	"github.com/thrgamon/go-utils/web/authentication"
	"github.com/thrgamon/nous/repo"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"github.com/jackc/pgx/v4/pgxpool"
)

type Environment int

const (
	Production Environment = iota + 1
	Development
)

var DB *pgxpool.Pool
var Templates map[string]*template.Template
var Logger *log.Logger
var Store *sessions.CookieStore
var ENV Environment

func main() {
	if env.GetEnvWithFallback("ENV", "production") == "development" {
		ENV = Development
	} else {
		ENV = Production
	}

	DB = initDB()
	defer DB.Close()

	cacheTemplates()

	Logger = log.New(os.Stdout, "logger: ", log.Lshortfile)

	Store = sessions.NewCookieStore([]byte(os.Getenv("SESSION_KEY")))
	authentication.Logger = Logger
	authentication.UserRepo = urepo.NewUserRepo(DB)
	authentication.Store = Store 

	r := mux.NewRouter()
	r.HandleFunc("/login", authentication.LoginHandler)
	r.HandleFunc("/logout", authentication.Logout)
	r.HandleFunc("/callback", authentication.CallbackHandler)
  authedRouter := r.NewRoute().Subrouter()
	authedRouter.Use(ensureAuthed)
	authedRouter.HandleFunc("/", HomeHandler)

	authedRouter.HandleFunc("/t/{date}", HomeSinceHandler)
	authedRouter.HandleFunc("/submit", SubmitHandler)
	authedRouter.HandleFunc("/search", SearchHandler)
	authedRouter.PathPrefix("/public/").HandlerFunc(serveResources)
	authedRouter.HandleFunc("/note", AddNoteHandler)
	authedRouter.HandleFunc("/note/{id:[0-9]+}/delete", DeleteNoteHandler)
	authedRouter.HandleFunc("/note/toggle", ToggleNoteHandler)

	srv := &http.Server{
		Handler:      handlers.CombinedLoggingHandler(os.Stdout, r),
		Addr:         "0.0.0.0:" + env.GetEnvWithFallback("PORT", "8080"),
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}

	Logger.Println("Server listening")
	log.Fatal(srv.ListenAndServe())
}

type PageData struct {
	Notes []repo.Note
}

func HomeSinceHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	date := vars["date"]

	parsedTime, err := time.Parse(time.RFC3339, date+"T00:00:00+11:00")

	if err != nil {
		handleUnexpectedError(w, err)
		return
	}

	noteRepo := repo.NewNoteRepo(DB)
	notes, err := noteRepo.GetAllSince(r.Context(), parsedTime)

	if err != nil {
		handleUnexpectedError(w, err)
		return
	}

	pageData := PageData{Notes: notes}

	RenderTemplate(w, "home", pageData)
}

func HomeHandler(w http.ResponseWriter, r *http.Request) {
	noteRepo := repo.NewNoteRepo(DB)
	notes, err := noteRepo.GetAllSince(r.Context(), time.Now())

	if err != nil {
		handleUnexpectedError(w, err)
		return
	}

	pageData := PageData{Notes: notes}

	RenderTemplate(w, "home", pageData)
}

func SubmitHandler(w http.ResponseWriter, r *http.Request) {
	RenderTemplate(w, "submit", PageData{})
}

func ViewNoteHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	noteId := vars["noteId"]

	noteRepo := repo.NewNoteRepo(DB)
	note, err := noteRepo.Get(r.Context(), repo.NoteID(noteId))

	if err != nil {
		handleUnexpectedError(w, err)
		return
	}

	pageData := PageData{Notes: []repo.Note{note}}
	RenderTemplate(w, "view", pageData)
}

func ToggleNoteHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	id := r.FormValue("id")

	noteRepo := repo.NewNoteRepo(DB)
	err := noteRepo.ToggleDone(r.Context(), repo.NoteID(id))

	if err != nil {
		handleUnexpectedError(w, err)
		return
	}

	http.Redirect(w, r, "/#"+id, http.StatusSeeOther)
}

func DeleteNoteHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	noteRepo := repo.NewNoteRepo(DB)
	err := noteRepo.Delete(r.Context(), repo.NoteID(id))

	if err != nil {
		handleUnexpectedError(w, err)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func AddNoteHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	body := r.FormValue("body")
	tags := r.FormValue("tags")

	noteRepo := repo.NewNoteRepo(DB)
	err := noteRepo.Add(r.Context(), body, tags)

	if err != nil {
		handleUnexpectedError(w, err)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func SearchHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	query := r.FormValue("query")

	noteRepo := repo.NewNoteRepo(DB)
	notes, err := noteRepo.Search(r.Context(), query)

	if err != nil {
		handleUnexpectedError(w, err)
		return
	}

	pageData := PageData{Notes: notes}

	RenderTemplate(w, "home", pageData)
}

func RenderTemplate(w http.ResponseWriter, tmpl string, data interface{}) {
	// In production we want to read the cached templates, whereas in development
	// we want to interpret them every time to make it easier to change
	if ENV == Production {
		err := Templates[tmpl].Execute(w, data)

		if err != nil {
			handleUnexpectedError(w, err)
			return
		}
	} else {
		template := template.Must(template.ParseFiles("views/"+tmpl+".html", "views/_header.html", "views/_footer.html"))
		err := template.Execute(w, data)

		if err != nil {
			handleUnexpectedError(w, err)
			return
		}
	}
}

func cacheTemplates() {
	re := regexp.MustCompile(`^[a-zA-Z\/]*\.html`)
	templates := make(map[string]*template.Template)
	// Walk the template directory and parse all templates that aren't fragments
	err := filepath.WalkDir("views",
		func(path string, info fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if re.MatchString(path) {
				normalisedPath := strings.TrimSuffix(strings.TrimPrefix(path, "views/"), ".html")
				templates[normalisedPath] = template.Must(
					template.ParseFiles(path, "views/_header.html", "views/_footer.html"),
				)
			}

			return nil
		})

	if err != nil {
		log.Fatal(err.Error())
	}

	// Assign to global variable so we can access it when rendering templates
	Templates = templates

}

func initDB() *pgxpool.Pool {
	conn, err := pgxpool.Connect(context.Background(), os.Getenv("DATABASE_URL"))

	if err != nil {
		log.Fatal(err)
	}

	return conn
}

// Handler for serving static assets with modified time to help
// caching
func serveResources(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(filepath.Join(".", r.URL.Path))
	if err != nil {
		http.Error(w, r.RequestURI, http.StatusNotFound)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		http.Error(w, r.RequestURI, http.StatusNotFound)
		return
	}
	modTime := fi.ModTime()

	http.ServeContent(w, r, r.URL.Path, modTime, f)
}

func handleUnexpectedError(w http.ResponseWriter, err error) {
	http.Error(w, "There was an unexpected error", http.StatusInternalServerError)
	Logger.Println(err.Error())
}

func getUserFromSession(r *http.Request) (urepo.User, bool) {
	sessionState, err := Store.Get(r, "auth")
  if err !=  nil {
    println(err.Error())
  }
	userRepo := urepo.NewUserRepo(DB)
	userId, ok := sessionState.Values["user_id"].(string)
  Logger.Printf("%v", sessionState.Values)

	if ok {
		user, _ := userRepo.Get(r.Context(), urepo.Auth0ID(userId))
		return user, true
	} else {
		return urepo.User{}, false
	}
}

func ensureAuthed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok := getUserFromSession(r)
		if ok {
			next.ServeHTTP(w, r)
		} else {
	    http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
			return
		}
	})
}
