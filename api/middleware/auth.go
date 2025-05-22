package middleware

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/zxmrlc/log"
	"go_email/pkg/errno"
	"go_email/pkg/token"
	"go_email/pkg/utils"
	"regexp"
)

var (
	// ErrMissingHeader means the `token` header was empty.
	ErrMissingHeader = errors.New("The length of the `token` header is zero. ")
)

// ParseRequest gets the token from the header and
// pass it to the Parse function to parses the token.
func ParseRequest(c *gin.Context) (int, error) {
	header := c.Request.Header.Get("token")

	if len(header) == 0 {
		return 0, ErrMissingHeader
	}

	tokenClaims, err := token.ParseToken(header)

	if err != nil {
		return 0, errno.ErrTokenInvalid
	}
	if tokenClaims.UserId == 0 {
		return 0, errno.ErrTokenInvalid
	}
	return tokenClaims.UserId, err
}

func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		//tokenString, _ := token.GenerateToken(1)
		//c.Request.Header.Set("token", tokenString)

		// Parse the token.
		UserId, err := ParseRequest(c)
		if err != nil {
			//try login
			path := c.Request.URL.Path
			// if it's not login, return ErrTokenInvalid
			reg := regexp.MustCompile("(/login|/review|/payWeChat|/test|/getVersion)")
			if !reg.MatchString(path) {
				log.Infof("Auth Failed %s %v", path, c.Request.Header)
				utils.SendResponse(c, errno.ErrTokenInvalid, nil)
				c.Abort()
				return
			}

		} else {
			// if it's valid taoken, keep UserId in context
			c.Set("UserId", UserId)
		}
		c.Next()
	}
}
