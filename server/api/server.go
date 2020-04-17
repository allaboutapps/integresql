package api

import "github.com/allaboutapps/integresql/pgtestpool"

type Server struct {
	M      *pgtestpool.Manager
	Config ServerConfig
}
