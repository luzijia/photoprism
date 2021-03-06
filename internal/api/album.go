package api

import (
	"archive/zip"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/photoprism/photoprism/internal/entity"
	"github.com/photoprism/photoprism/internal/event"
	"github.com/photoprism/photoprism/internal/form"
	"github.com/photoprism/photoprism/internal/query"
	"github.com/photoprism/photoprism/internal/service"
	"github.com/photoprism/photoprism/internal/thumb"
	"github.com/photoprism/photoprism/pkg/fs"
	"github.com/photoprism/photoprism/pkg/rnd"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/photoprism/photoprism/internal/config"
	"github.com/photoprism/photoprism/pkg/txt"
)

// GET /api/v1/albums
func GetAlbums(router *gin.RouterGroup, conf *config.Config) {
	router.GET("/albums", func(c *gin.Context) {
		if Unauthorized(c, conf) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		var f form.AlbumSearch

		err := c.MustBindWith(&f, binding.Form)

		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": txt.UcFirst(err.Error())})
			return
		}

		result, err := query.AlbumSearch(f)
		if err != nil {
			c.AbortWithStatusJSON(400, gin.H{"error": txt.UcFirst(err.Error())})
			return
		}

		// TODO c.Header("X-Count", strconv.Itoa(count))
		c.Header("X-Limit", strconv.Itoa(f.Count))
		c.Header("X-Offset", strconv.Itoa(f.Offset))

		c.JSON(http.StatusOK, result)
	})
}

// GET /api/v1/albums/:uid
func GetAlbum(router *gin.RouterGroup, conf *config.Config) {
	router.GET("/albums/:uid", func(c *gin.Context) {
		id := c.Param("uid")
		m, err := query.AlbumByUID(id)

		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, ErrAlbumNotFound)
			return
		}

		c.JSON(http.StatusOK, m)
	})
}

// POST /api/v1/albums
func CreateAlbum(router *gin.RouterGroup, conf *config.Config) {
	router.POST("/albums", func(c *gin.Context) {
		if Unauthorized(c, conf) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		var f form.Album

		if err := c.BindJSON(&f); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": txt.UcFirst(err.Error())})
			return
		}

		m := entity.NewAlbum(f.AlbumTitle, entity.TypeDefault)
		m.AlbumFavorite = f.AlbumFavorite

		log.Debugf("create album: %+v %+v", f, m)

		if res := entity.Db().Create(m); res.Error != nil {
			log.Error(res.Error.Error())
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s already exists", txt.Quote(m.AlbumTitle))})
			return
		}

		event.Success("album created")

		UpdateClientConfig(conf)

		PublishAlbumEvent(EntityCreated, m.AlbumUID, c)

		c.JSON(http.StatusOK, m)
	})
}

// PUT /api/v1/albums/:uid
func UpdateAlbum(router *gin.RouterGroup, conf *config.Config) {
	router.PUT("/albums/:uid", func(c *gin.Context) {
		if Unauthorized(c, conf) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		uid := c.Param("uid")
		m, err := query.AlbumByUID(uid)

		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, ErrAlbumNotFound)
			return
		}

		f, err := form.NewAlbum(m)

		if err != nil {
			log.Error(err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, ErrSaveFailed)
			return
		}

		if err := c.BindJSON(&f); err != nil {
			log.Error(err)
			c.AbortWithStatusJSON(http.StatusBadRequest, ErrFormInvalid)
			return
		}

		if err := m.SaveForm(f); err != nil {
			log.Error(err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, ErrSaveFailed)
			return
		}

		UpdateClientConfig(conf)

		event.Success("album saved")

		PublishAlbumEvent(EntityUpdated, uid, c)

		c.JSON(http.StatusOK, m)
	})
}

// DELETE /api/v1/albums/:uid
func DeleteAlbum(router *gin.RouterGroup, conf *config.Config) {
	router.DELETE("/albums/:uid", func(c *gin.Context) {
		if Unauthorized(c, conf) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		id := c.Param("uid")

		m, err := query.AlbumByUID(id)

		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, ErrAlbumNotFound)
			return
		}

		PublishAlbumEvent(EntityDeleted, id, c)

		conf.Db().Delete(&m)

		UpdateClientConfig(conf)
		event.Success(fmt.Sprintf("album %s deleted", txt.Quote(m.AlbumTitle)))

		c.JSON(http.StatusOK, m)
	})
}

// POST /api/v1/albums/:uid/like
//
// Parameters:
//   uid: string Album UID
func LikeAlbum(router *gin.RouterGroup, conf *config.Config) {
	router.POST("/albums/:uid/like", func(c *gin.Context) {
		if Unauthorized(c, conf) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		id := c.Param("uid")
		album, err := query.AlbumByUID(id)

		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, ErrAlbumNotFound)
			return
		}

		album.AlbumFavorite = true
		conf.Db().Save(&album)

		UpdateClientConfig(conf)
		PublishAlbumEvent(EntityUpdated, id, c)

		c.JSON(http.StatusOK, http.Response{})
	})
}

// DELETE /api/v1/albums/:uid/like
//
// Parameters:
//   uid: string Album UID
func DislikeAlbum(router *gin.RouterGroup, conf *config.Config) {
	router.DELETE("/albums/:uid/like", func(c *gin.Context) {
		if Unauthorized(c, conf) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		id := c.Param("uid")
		album, err := query.AlbumByUID(id)

		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, ErrAlbumNotFound)
			return
		}

		album.AlbumFavorite = false
		conf.Db().Save(&album)

		UpdateClientConfig(conf)
		PublishAlbumEvent(EntityUpdated, id, c)

		c.JSON(http.StatusOK, http.Response{})
	})
}

// POST /api/v1/albums/:uid/photos
func AddPhotosToAlbum(router *gin.RouterGroup, conf *config.Config) {
	router.POST("/albums/:uid/photos", func(c *gin.Context) {
		if Unauthorized(c, conf) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		var f form.Selection

		if err := c.BindJSON(&f); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": txt.UcFirst(err.Error())})
			return
		}

		uid := c.Param("uid")
		a, err := query.AlbumByUID(uid)

		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, ErrAlbumNotFound)
			return
		}

		photos, err := query.PhotoSelection(f)

		if err != nil {
			log.Errorf("album: %s", err)
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": txt.UcFirst(err.Error())})
			return
		}

		var added []*entity.PhotoAlbum

		for _, p := range photos {
			val := entity.FirstOrCreatePhotoAlbum(entity.NewPhotoAlbum(p.PhotoUID, a.AlbumUID))

			if val != nil {
				added = append(added, val)
			}
		}

		if len(added) == 1 {
			event.Success(fmt.Sprintf("one photo added to %s", txt.Quote(a.AlbumTitle)))
		} else {
			event.Success(fmt.Sprintf("%d photos added to %s", len(added), txt.Quote(a.AlbumTitle)))
		}

		PublishAlbumEvent(EntityUpdated, a.AlbumUID, c)

		c.JSON(http.StatusOK, gin.H{"message": "photos added to album", "album": a, "added": added})
	})
}

// DELETE /api/v1/albums/:uid/photos
func RemovePhotosFromAlbum(router *gin.RouterGroup, conf *config.Config) {
	router.DELETE("/albums/:uid/photos", func(c *gin.Context) {
		if Unauthorized(c, conf) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		var f form.Selection

		if err := c.BindJSON(&f); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": txt.UcFirst(err.Error())})
			return
		}

		if len(f.Photos) == 0 {
			log.Error("no photos selected")
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": txt.UcFirst("no photos selected")})
			return
		}

		a, err := query.AlbumByUID(c.Param("uid"))

		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, ErrAlbumNotFound)
			return
		}

		entity.Db().Where("album_uid = ? AND photo_uid IN (?)", a.AlbumUID, f.Photos).Delete(&entity.PhotoAlbum{})

		event.Success(fmt.Sprintf("photos removed from %s", a.AlbumTitle))

		PublishAlbumEvent(EntityUpdated, a.AlbumUID, c)

		c.JSON(http.StatusOK, gin.H{"message": "photos removed from album", "album": a, "photos": f.Photos})
	})
}

// GET /albums/:uid/dl
func DownloadAlbum(router *gin.RouterGroup, conf *config.Config) {
	router.GET("/albums/:uid/dl", func(c *gin.Context) {
		if InvalidDownloadToken(c, conf) {
			c.Data(http.StatusForbidden, "image/svg+xml", brokenIconSvg)
			return
		}

		start := time.Now()

		a, err := query.AlbumByUID(c.Param("uid"))

		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, ErrAlbumNotFound)
			return
		}

		p, _, err := query.PhotoSearch(form.PhotoSearch{
			Album:  a.AlbumUID,
			Count:  10000,
			Offset: 0,
		})

		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": txt.UcFirst(err.Error())})
			return
		}

		zipPath := path.Join(conf.TempPath(), "album")
		zipToken := rnd.Token(3)
		zipBaseName := fmt.Sprintf("%s-%s.zip", strings.Title(a.AlbumSlug), zipToken)
		zipFileName := path.Join(zipPath, zipBaseName)

		if err := os.MkdirAll(zipPath, 0700); err != nil {
			log.Error(err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": txt.UcFirst("failed to create zip folder")})
			return
		}

		newZipFile, err := os.Create(zipFileName)

		if err != nil {
			log.Error(err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": txt.UcFirst(err.Error())})
			return
		}

		defer newZipFile.Close()

		zipWriter := zip.NewWriter(newZipFile)
		defer func() { _ = zipWriter.Close() }()

		for _, f := range p {
			fileName := path.Join(conf.OriginalsPath(), f.FileName)
			fileAlias := f.ShareFileName()

			if fs.FileExists(fileName) {
				if err := addFileToZip(zipWriter, fileName, fileAlias); err != nil {
					log.Error(err)
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": txt.UcFirst("failed to create zip file")})
					return
				}
				log.Infof("album: added %s as %s", txt.Quote(f.FileName), txt.Quote(fileAlias))
			} else {
				log.Errorf("album: file %s is missing", txt.Quote(f.FileName))
			}
		}

		log.Infof("album: archive %s created in %s", txt.Quote(zipBaseName), time.Since(start))
		_ = zipWriter.Close()
		newZipFile.Close()

		if !fs.FileExists(zipFileName) {
			log.Errorf("could not find zip file: %s", zipFileName)
			c.Data(http.StatusNotFound, "image/svg+xml", photoIconSvg)
			return
		}

		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", zipBaseName))

		c.File(zipFileName)

		if err := os.Remove(zipFileName); err != nil {
			log.Errorf("album: could not remove %s (%s)", txt.Quote(zipFileName), err.Error())
		}
	})
}

// GET /api/v1/albums/:uid/t/:token/:type
//
// Parameters:
//   uid: string Album UID
//   type: string Thumbnail type, see photoprism.ThumbnailTypes
func AlbumThumbnail(router *gin.RouterGroup, conf *config.Config) {
	router.GET("/albums/:uid/t/:token/:type", func(c *gin.Context) {
		if InvalidToken(c, conf) {
			c.Data(http.StatusForbidden, "image/svg+xml", brokenIconSvg)
			return
		}

		typeName := c.Param("type")
		uid := c.Param("uid")
		start := time.Now()

		thumbType, ok := thumb.Types[typeName]

		if !ok {
			log.Errorf("album: invalid thumb type %s", typeName)
			c.Data(http.StatusOK, "image/svg+xml", photoIconSvg)
			return
		}

		gc := service.Cache()
		cacheKey := fmt.Sprintf("album-thumbnail:%s:%s", uid, typeName)

		if cacheData, ok := gc.Get(cacheKey); ok {
			log.Debugf("cache hit for %s [%s]", cacheKey, time.Since(start))
			c.Data(http.StatusOK, "image/jpeg", cacheData.([]byte))
			return
		}

		f, err := query.AlbumThumbByUID(uid)

		if err != nil {
			log.Debugf("album: no photos yet, using generic image for %s", uid)
			c.Data(http.StatusOK, "image/svg+xml", albumIconSvg)
			return
		}

		fileName := path.Join(conf.OriginalsPath(), f.FileName)

		if !fs.FileExists(fileName) {
			log.Errorf("album: could not find original for %s", fileName)
			c.Data(http.StatusOK, "image/svg+xml", photoIconSvg)

			// Set missing flag so that the file doesn't show up in search results anymore.
			log.Warnf("album: %s is missing", txt.Quote(f.FileName))
			report("album", f.Update("FileMissing", true))
			return
		}

		// Use original file if thumb size exceeds limit, see https://github.com/photoprism/photoprism/issues/157
		if thumbType.ExceedsLimit() && c.Query("download") == "" {
			log.Debugf("album: using original, thumbnail size exceeds limit (width %d, height %d)", thumbType.Width, thumbType.Height)
			c.File(fileName)
			return
		}

		var thumbnail string

		if conf.ThumbUncached() || thumbType.OnDemand() {
			thumbnail, err = thumb.FromFile(fileName, f.FileHash, conf.ThumbPath(), thumbType.Width, thumbType.Height, thumbType.Options...)
		} else {
			thumbnail, err = thumb.FromCache(fileName, f.FileHash, conf.ThumbPath(), thumbType.Width, thumbType.Height, thumbType.Options...)
		}

		if err != nil {
			log.Errorf("album: %s", err)
			c.Data(http.StatusOK, "image/svg+xml", photoIconSvg)
			return
		}

		if c.Query("download") != "" {
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", f.ShareFileName()))
		}

		thumbData, err := ioutil.ReadFile(thumbnail)

		if err != nil {
			log.Errorf("album: %s", err)
			c.Data(http.StatusOK, "image/svg+xml", albumIconSvg)
			return
		}

		gc.Set(cacheKey, thumbData, time.Hour)

		log.Debugf("cached %s [%s]", cacheKey, time.Since(start))

		c.Data(http.StatusOK, "image/jpeg", thumbData)
	})
}
