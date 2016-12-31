package gphotos

import (
	"net/http"

	"google.golang.org/api/drive/v3"
)

// Photos returns a channel which will receive all the photos metadata
// in batches.
//
// The client must be prepared with drive.DrivePhotosReadonlyScope
// ("https://www.googleapis.com/auth/drive.photos.readonly").
//
// If sinceToken is not empty, only the changed photos are returned.
//
// Returns a new token to watch future changes.
func Photos(client *http.Client, sinceToken string) (<-chan MaybeFiles, string, error) {
	srv, err := drive.New(client)
	if err != nil {
		return nil, "", err
	}

	sr, err := srv.Changes.GetStartPageToken().Do()
	if err != nil {
		return nil, "", err
	}
	nextToken := sr.StartPageToken

	const fields = "nextPageToken, files(id,name,mimeType,description,starred,parents,properties,webContentLink,createdTime,modifiedTime,owners,originalFilename,imageMediaMetadata)"

	if sinceToken != "" {
		L := func(token string) *drive.ChangesListCall {
			return srv.Changes.List(token).
				Fields(fields).
				Spaces("photos").
				PageSize(1000).
				IncludeRemoved(false)
		}

		r, err := L(sinceToken).Do()
		if err != nil {
			return nil, "", err
		}
		ch := make(chan MaybeFiles)
		go func() {
			defer close(ch)
			for {
				files := make([]*drive.File, 0, len(r.Changes))
				for _, c := range r.Changes {
					if c.File != nil {
						files = append(files, c.File)
					}
				}
				if len(files) > 0 {
					ch <- MaybeFiles{Files: files}
				}
				if r.NextPageToken == "" {
					return
				}
				if r, err = L(r.NextPageToken).Do(); err != nil {
					ch <- MaybeFiles{Err: err}
					return
				}
			}
		}()
		return ch, nextToken, err
	}

	listCall := srv.Files.List().
		Fields(fields).
		Spaces("photos").
		PageSize(1000)

	r, err := listCall.Do()
	if err != nil {
		return nil, nextToken, err
	}
	ch := make(chan MaybeFiles)
	go func() {
		defer close(ch)
		for {
			ch <- MaybeFiles{Files: r.Files}

			if r.NextPageToken == "" {
				return
			}
			if r, err = listCall.PageToken(r.NextPageToken).Do(); err != nil {
				ch <- MaybeFiles{Err: err}
				return
			}
		}
	}()

	return ch, nextToken, nil
}

// MaybeFiles may contain files slice, or an error.
type MaybeFiles struct {
	Files []*drive.File
	Err   error
}
