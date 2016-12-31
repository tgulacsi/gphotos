package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/tgulacsi/gphotos"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
)

// getClient uses a Context and Config to retrieve a Token
// then generate a Client. It returns the generated Client.
func getClient(ctx context.Context, config *oauth2.Config) (*http.Client, error) {
	cacheFile, err := tokenCacheFile()
	if err != nil {
		return nil, err
	}
	tok, err := tokenFromFile(cacheFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(cacheFile, tok)
	}
	return config.Client(ctx, tok), nil
}

// getTokenFromWeb uses Config to request a Token.
// It returns the retrieved Token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var code string
	if _, err := fmt.Scan(&code); err != nil {
		log.Fatalf("Unable to read authorization code %v", err)
	}

	tok, err := config.Exchange(oauth2.NoContext, code)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web %v", err)
	}
	return tok
}

// tokenCacheFile generates credential file path/filename.
// It returns the generated credential path/filename.
func tokenCacheFile() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	tokenCacheDir := filepath.Join(usr.HomeDir, ".credentials")
	os.MkdirAll(tokenCacheDir, 0700)
	return filepath.Join(tokenCacheDir,
		url.QueryEscape("drive-go-quickstart.json")), err
}

// tokenFromFile retrieves a Token from a given file path.
// It returns the retrieved Token and any read error encountered.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	t := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(t)
	defer f.Close()
	return t, err
}

// saveToken uses a file path to create a file and store the
// token in it.
func saveToken(file string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", file)
	f, err := os.Create(file)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

type Flags struct {
	ClientID, ClientSecret string
	SecretJSON             string
}

func NewFlagSet(cfg *Flags) *flag.FlagSet {
	set := flag.NewFlagSet("gphotos", flag.ContinueOnError)
	set.StringVar(&cfg.ClientID, "id", cfg.ClientID, "Client ID")
	set.StringVar(&cfg.ClientSecret, "secret", cfg.ClientSecret, "Client secret")
	set.StringVar(&cfg.SecretJSON, "secret-json", cfg.SecretJSON, "client_secret.json, as downloaded from console.developers.google.com")
	return set
}

func (cfg Flags) GetConfig() (*oauth2.Config, error) {
	var config *oauth2.Config
	if cfg.SecretJSON != "" {
		b, err := ioutil.ReadFile(cfg.SecretJSON)
		if err != nil {
			log.Fatalf("Unable to read client secret file: %v", err)
		}
		if config, err = google.ConfigFromJSON(b, drive.DrivePhotosReadonlyScope); err == nil {
			return config, nil
		}
		if cfg.ClientID == "" {
			return nil, err
		}
	}
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{drive.DrivePhotosReadonlyScope},
		RedirectURL:  "urn:ietf:wg:oauth:2.0:oob",
	}, nil
}
func (cfg Flags) GetClient(ctx context.Context) (*http.Client, error) {
	config, err := cfg.GetConfig()
	if err != nil {
		return nil, err
	}
	return getClient(ctx, config)
}

func main() {
	var cfg Flags
	flags := NewFlagSet(&cfg)
	flags.Parse(os.Args[1:])

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := cfg.GetClient(ctx)
	if err != nil {
		log.Fatal(err)
	}

	ch, next, err := gphotos.Photos(client, "")
	if err != nil {
		log.Fatal(err)
	}
	log.Println("next:", next)
	for mf := range ch {
		if mf.Err != nil {
			log.Fatal(mf.Err)
		}
		if len(mf.Files) == 0 {
			continue
		}
		log.Printf("%d files, first: %#v", len(mf.Files), mf.Files[0])
	}
}
