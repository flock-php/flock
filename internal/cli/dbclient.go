package cli

// defaultDBClient is the command `flock db` opens the database with when it wasn't chosen: the
// scheme first, so the user's own default client gets the URL; an installed client the system
// doesn't route to second; "" prints the URL instead.
func defaultDBClient() string {
	if opener := openScheme(); opener != "" {
		return opener
	}
	return clientFallback()
}
