package ui

import (
	"github.com/dcaceresc/pulso/internal/config"
	"github.com/dcaceresc/pulso/internal/soloist"
	"github.com/dcaceresc/pulso/internal/spotify"
)

// Alias transitorios: mantienen el código actual del workspace y sus pruebas
// con los nombres que ya usan mientras los tipos concretos viven en sus nuevos
// paquetes. No añaden comportamiento y pueden retirarse cuando cada referencia
// use directamente el paquete propietario.
type (
	playerState     = soloist.State
	playerCommand   = soloist.Command
	playerTrack     = soloist.Track
	catalogItem     = spotify.Item
	libraryTrack    = spotify.Track
	libraryPlaylist = spotify.Playlist
	preferences     = config.Preferences
	themePalette    = config.Palette
)

const spotifyCatalogPageSize = spotify.CatalogPageSize

var errPlaylistUnavailable = spotify.ErrPlaylistUnavailable
var errSpotifyCatalog = spotify.ErrSpotifyCatalog
