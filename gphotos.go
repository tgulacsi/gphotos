package gphotos

import (
	"net/http"
	"time"

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
func Photos(client *http.Client, sinceToken string) (<-chan MaybePhotos, string, error) {
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
		ch := make(chan MaybePhotos)
		go func() {
			defer close(ch)
			for {
				photos := make([]Photo, 0, len(r.Changes))
				for _, c := range r.Changes {
					if c.File != nil {
						photos = append(photos, fileAsPhoto(c.File))
					}
				}
				if len(photos) > 0 {
					ch <- MaybePhotos{Photos: photos}
				}
				if r.NextPageToken == "" {
					return
				}
				if r, err = L(r.NextPageToken).Do(); err != nil {
					ch <- MaybePhotos{Err: err}
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
	ch := make(chan MaybePhotos)
	go func() {
		defer close(ch)
		for {
			photos := make([]Photo, 0, len(r.Files))
			for _, f := range r.Files {
				photos = append(photos, fileAsPhoto(f))
			}
			ch <- MaybePhotos{Photos: photos}

			if r.NextPageToken == "" {
				return
			}
			if r, err = listCall.PageToken(r.NextPageToken).Do(); err != nil {
				ch <- MaybePhotos{Err: err}
				return
			}
		}
	}()

	return ch, nextToken, nil
}

// MaybeFiles may contain files slice, or an error.
type MaybePhotos struct {
	Photos []Photo
	Err    error
}

type Photo struct {
	ID                          string
	Name, MimeType, Description string
	Starred                     bool
	Parents                     []string
	Properties                  map[string]string
	WebContentLink              string
	CreatedTime, ModifiedTime   time.Time
	OriginalFilename            string
	drive.FileImageMediaMetadata
}

func fileAsPhoto(f *drive.File) Photo {
	if f == nil {
		return Photo{}
	}
	p := Photo{
		ID:               f.Id,
		Name:             f.Name,
		MimeType:         f.MimeType,
		Description:      f.Description,
		Starred:          f.Starred,
		Parents:          f.Parents,
		Properties:       f.Properties,
		WebContentLink:   f.WebContentLink,
		OriginalFilename: f.OriginalFilename,
	}
	if f.ImageMediaMetadata != nil {
		p.FileImageMediaMetadata = *f.ImageMediaMetadata
	}
	if f.CreatedTime != "" {
		p.CreatedTime, _ = time.Parse(time.RFC3339, f.CreatedTime)
	}
	if f.ModifiedTime != "" {
		p.ModifiedTime, _ = time.Parse(time.RFC3339, f.ModifiedTime)
	}
	return p
}
