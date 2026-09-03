package api

import (
	"fmt"
	"net/http"
)

func (app *Application) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "status: available")
	fmt.Fprintf(w, "environment: %s\n", app.Env)
	fmt.Fprintf(w, "version: %s\n", app.Version)
}
