package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	"github.com/samber/lo"
	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
)

const redirectURI = "http://localhost:8080/spotifycallback"

var (
	auth = spotifyauth.New(
		spotifyauth.WithRedirectURL(redirectURI),
		spotifyauth.WithClientID(os.Getenv("SPOTIFY_CLIENT_ID")),
		spotifyauth.WithClientSecret(os.Getenv("SPOTIFY_CLIENT_SECRET")),
		spotifyauth.WithScopes(spotifyauth.ScopePlaylistReadPrivate, spotifyauth.ScopePlaylistReadCollaborative, spotifyauth.ScopePlaylistModifyPrivate))
	ch    = make(chan *spotify.Client)
	state = "abc123"

	uid          = ""
	playlistName = flag.String("playlist", "", "playlist name")
	dry          = flag.Bool("dry", false, "dry")
)

func main() {
	ctx := context.Background()

	flag.Parse()
	if *playlistName == "" {
		log.Fatalln("no playlist name provided")
	}

	// first start an HTTP server
	http.HandleFunc("/spotifycallback", completeAuth)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Got request for:", r.URL.String())
	})
	go func() {
		err := http.ListenAndServe(":8080", nil)
		if err != nil {
			log.Fatal(err)
		}
	}()

	url := auth.AuthURL(state)
	fmt.Println("Please log in to Spotify by visiting the following page in your browser:", url)

	// wait for auth to complete
	client := <-ch

	// use the client to make calls that require authorization
	user, err := client.CurrentUser(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("You are logged in as:", user.ID)
	uid = user.ID

	err = run(ctx, client)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("yay")
}

type plan struct {
	From  int
	Moved int
}

type savedPlaylist struct {
	Playlist spotify.SimplePlaylist
	Items    []spotify.PlaylistItem
}

func run(ctx context.Context, c *spotify.Client) error {
	sp, err := loadPlaylist(ctx, c, *playlistName)
	if err != nil {
		return fmt.Errorf("error in playlist load: %w", err)
	}

	ps := makePlans(sp)

	if *dry {
		log.Println("exit early dry run")
		return nil
	}

	ss := sp.Playlist.SnapshotID
	for psi, p := range ps {
		log.Printf("Reorder: %d/%d", psi+1, len(ps))
		ss, err = c.ReorderPlaylistTracks(ctx, sp.Playlist.ID, spotify.PlaylistReorderOptions{
			SnapshotID:   ss,
			RangeStart:   spotify.Numeric(p.From),
			RangeLength:  1,
			InsertBefore: spotify.Numeric(len(sp.Items)), // Move to back
		})
		if err != nil {
			// todo make stored playlist invalid now
			return fmt.Errorf("failed to reorder, delete cached playlist please: %w", err)
		}
	}

	return nil
}

func makePlans(sp *savedPlaylist) []*plan {
	type itemWithIndex struct {
		Item  spotify.PlaylistItem
		Index int
	}

	iis := lo.Map(sp.Items, func(item spotify.PlaylistItem, idx int) itemWithIndex {
		return itemWithIndex{
			Item:  item,
			Index: idx,
		}
	})

	il := lo.Interleave(lo.PartitionBy(iis, func(ii itemWithIndex) string {
		return ii.Item.AddedBy.ID
	})...)

	plans := lo.Map(il, func(ii itemWithIndex, newIdx int) *plan {
		return &plan{
			From:  ii.Index,
			Moved: -1,
		}
	})

	simulateMoves(plans)

	return plans
}

// Since spotify does not to bulk re order I need to simulate moving stuff around
// because the From index used in the API call keeps changing when other stuff is moved.

// This function simulates this move and makes sure the From index when the API call is made
// takes into account all the moves that came before it.
func simulateMoves(ps []*plan) {
	for i1, p1 := range ps {
		// move all other items that are below this one up now that we have moved this one to the back
		for i2, p2 := range ps {
			if i1 == i2 {
				continue
			}
			if p2.Moved != -1 {
				continue // no need to move up, it has been moved
			}
			if p2.From > p1.From { // p2.From can never be 0 because it is bigger than p1.From
				p2.From--
			}
		}
		p1.Moved = len(ps)
	}
}

func loadPlaylistLocal(name string) (*savedPlaylist, bool) {
	dir := lo.Must(os.ReadDir("."))
	e, ok := lo.Find(dir, func(e fs.DirEntry) bool {
		return e.Name() == fmt.Sprintf("playlist-%s.json", name)
	})
	if !ok {
		return nil, false
	}

	plis := savedPlaylist{}
	lo.Must0(json.Unmarshal(lo.Must(os.ReadFile(e.Name())), &plis))
	return &plis, true
}

func loadPlaylist(ctx context.Context, c *spotify.Client, name string) (*savedPlaylist, error) {
	sp, ok := loadPlaylistLocal(name)
	if ok {
		log.Println("Loaded playlist locally")
		return sp, nil
	}

	log.Println("Searching playlist")
	pls, err := c.GetPlaylistsForUser(ctx, uid, spotify.Limit(50))
	if err != nil {
		return nil, fmt.Errorf("playlist search: %w", err)
	}
	// TODO pagination. I don't have >50 playlists so it works for now.

	pl, ok := lo.Find(pls.Playlists, func(pl spotify.SimplePlaylist) bool {
		return pl.Name == name
	})
	if !ok {
		return nil, fmt.Errorf("playlist not found. playlists: %s", string(lo.Must(json.Marshal(pls.Playlists))))
	}

	log.Printf("Loading playlist contents for %s", pl.ID.String())

	plis := make([]spotify.PlaylistItem, 0, 50)
	pliPage, err := c.GetPlaylistItems(ctx, pl.ID, spotify.Limit(50))
	if err != nil {
		return nil, fmt.Errorf("playlist load items: %w", err)
	}

	log.Printf("Loaded %d items", len(pliPage.Items))
	plis = append(plis, pliPage.Items...)

	for page := 1; ; page++ {
		err := c.NextPage(ctx, pliPage)
		if err == spotify.ErrNoMorePages {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to page playlist load: %w", err)
		}
		plis = append(plis, pliPage.Items...)
		log.Printf("Loaded %d more items", len(pliPage.Items))
	}

	log.Println("Done loading!")

	sp = &savedPlaylist{
		Playlist: pl,
		Items:    plis,
	}

	lo.Must0(os.WriteFile(fmt.Sprintf("playlist-%s.json", name), lo.Must(json.Marshal(sp)), 0644))
	return sp, nil
}

func completeAuth(w http.ResponseWriter, r *http.Request) {
	tok, err := auth.Token(r.Context(), state, r)
	if err != nil {
		http.Error(w, "Couldn't get token", http.StatusForbidden)
		log.Fatal(err)
	}
	if st := r.FormValue("state"); st != state {
		http.NotFound(w, r)
		log.Fatalf("State mismatch: %s != %s\n", st, state)
	}

	// use the token to get an authenticated client
	client := spotify.New(auth.Client(r.Context(), tok), spotify.WithRetry(true))
	fmt.Fprintf(w, "Login Completed!")
	ch <- client
}
