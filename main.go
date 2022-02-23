// (c) Copyright 2022, twitch-search Authors.
//
// Licensed under the terms of the GNU GPL License version 3.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/twitch"
)

const (
	apiBase        = "https://api.twitch.tv/helix"
	serverPort     = 9001
	oauth2Callback = "callback"
)

var (
	flagType = flag.String("type", "archive", "type of VoDs to show (all, upload, archive, highlight)")
	flagChan = flag.String("vod", "", "show VoDs from specific channel")
	flagLive = flag.Bool("live", false, "show live followed channels")
)

var clientCreds *struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

func main() {
	flag.Parse()

	if !*flagLive {
		if len(*flagChan) == 0 {
			die("Channel to query is empty\n")
		}
		*flagChan = strings.ToLower(*flagChan)
	}

	userDir, err := os.UserHomeDir()
	if err != nil {
		die("Getting user directory: %v\n", err)
	}

	tokenPath := path.Join(userDir, ".twitch-search.json")
	credsPath := path.Join(userDir, ".twitch-search-client.json")

	credsBs, err := os.ReadFile(credsPath)
	if err != nil {
		die("Reading client credentials from %q: %v\n", credsPath, err)
	}
	if err := json.Unmarshal(credsBs, &clientCreds); err != nil {
		die("Parsing client credentials from %q: %v\n", credsPath, err)
	}

	var token *oauth2.Token
	oauth2Config := oauth2.Config{
		ClientID:     clientCreds.ID,
		ClientSecret: clientCreds.Secret,
		Scopes:       []string{"user:read:follows"},
		RedirectURL:  fmt.Sprintf("http://localhost:%d/%s", serverPort, oauth2Callback),
		Endpoint:     twitch.Endpoint,
	}

	tokenBs, err := os.ReadFile(tokenPath)
	switch {
	case err == nil:
		var t oauth2.Token
		if err := json.Unmarshal(tokenBs, &t); err != nil {
			die("Parsing stored JSON in %q: %v\n", tokenPath, err)
		}
		token = &t
	case errors.Is(err, os.ErrNotExist):
		onCallback := make(chan struct{})
		serverH := http.NewServeMux()

		serverH.HandleFunc("/"+oauth2Callback, func(w http.ResponseWriter, r *http.Request) {
			t, err := oauth2Config.Exchange(context.Background(), r.FormValue("code"))
			if err != nil {
				die("Exchanging code for token: %v\n", err)
			}
			token = t
			close(onCallback)
		})

		server := http.Server{Addr: fmt.Sprintf("localhost:%d", serverPort), Handler: serverH}
		defer server.Close()
		go func() {
			if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				die("OAuth2 server exit: %v\n", err)
			}
		}()

		fmt.Printf("Open your browser to %s\n", oauth2Config.AuthCodeURL(""))
		<-onCallback
	default:
		die("Reading token file %q: %v\n", tokenPath, err)
	}

	tokenSource := oauth2Config.TokenSource(context.Background(), token)
	token, err = tokenSource.Token()
	if err != nil {
		die("Refreshing token: %v\n", err)
	}

	// store token for future use
	tokenBs, err = json.Marshal(token)
	if err != nil {
		die("Marshalling token: %v\n", err)
	}
	if err := os.WriteFile(tokenPath, tokenBs, os.FileMode(0600)); err != nil {
		die("Storing token to file %q: %v\n", tokenPath, err)
	}

	client := oauth2.NewClient(context.Background(), tokenSource)

	var searchRes struct {
		Data []struct {
			ID        string    `json:"id"`
			UserName  string    `json:"user_name"`
			UserLogin string    `json:"user_login"`
			Title     string    `json:"title"`
			URL       string    `json:"url"`        // URL is null when requesting followed streams
			CreatedAt time.Time `json:"created_at"` // same as above
		} `json:"data"`
	}

	if *flagLive {
		userID, err := loggedUserID(client)
		if err != nil {
			die("Getting logged user ID: %v\n", err)
		}

		respBs, err := makeReq(client, apiBase+"/streams/followed?user_id="+userID)
		if err != nil {
			die("Querying live channels: %v\n", err)
		}
		if err := json.Unmarshal(respBs, &searchRes); err != nil {
			die("Parsing JSON videos results: %v\n", err)
		}
		for _, v := range searchRes.Data {
			title := strings.ReplaceAll(v.Title, "\n", " ")
			fmt.Printf("%s %s\n", v.UserLogin, title)
		}
	} else {
		chanID, err := searchChannel(client, *flagChan)
		if err != nil {
			die("Searching channels: %v\n", err)
		}

		respBs, err := makeReq(client, apiBase+"/videos?first=100&type="+*flagType+"&user_id="+chanID)
		if err != nil {
			die("Searching videos: %v\n", err)
		}
		if err := json.Unmarshal(respBs, &searchRes); err != nil {
			die("Parsing JSON videos results: %v\n", err)
		}
		for _, v := range searchRes.Data {
			title := strings.ReplaceAll(v.Title, "\n", " ")
			fmt.Printf("%s %s %s\n", v.URL, v.CreatedAt.Format("2006-01-02"), title)
		}
	}
}

func die(fmtStr string, args ...interface{}) {
	fmt.Printf(fmtStr, args...)
	os.Exit(1)
}

func searchChannel(client *http.Client, channel string) (string, error) {
	respBs, err := makeReq(client, apiBase+"/search/channels?first=100&query="+channel)
	if err != nil {
		return "", err
	}
	var searchRes struct {
		Data []struct {
			ID    string `json:"id"`
			Login string `json:"broadcaster_login"`
			Name  string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBs, &searchRes); err != nil {
		return "", fmt.Errorf("parsing JSON: %v", err)
	}

	for _, r := range searchRes.Data {
		if strings.ToLower(r.Name) == channel || strings.ToLower(r.Login) == channel {
			return r.ID, nil
		}
	}
	return "", nil
}

func loggedUserID(client *http.Client) (string, error) {
	respBs, err := makeReq(client, apiBase+"/users")
	if err != nil {
		return "", err
	}
	var searchRes struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBs, &searchRes); err != nil {
		return "", fmt.Errorf("parsing JSON: %v", err)
	}

	return searchRes.Data[0].ID, nil
}

func makeReq(client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %v", err)
	}
	req.Header.Add("Client-ID", clientCreds.ID)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %v", err)
	}
	defer resp.Body.Close()
	respBs, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading reply: %v", err)
	}
	return respBs, nil
}
