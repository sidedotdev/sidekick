package api

import (
	"fmt"
	"html"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (ctrl *Controller) OpenAIOAuthCallbackHandler(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")

	pasteValue := ""
	if code != "" && state != "" {
		pasteValue = fmt.Sprintf("%s#%s", code, state)
	} else if code != "" {
		pasteValue = code
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><title>Sidekick – OpenAI OAuth</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1>OpenAI OAuth Callback</h1>
<p>Copy the value below and paste it into the <code>side auth</code> prompt:</p>
<pre style="background: #f4f4f4; padding: 12px; border-radius: 4px; word-break: break-all;">%s</pre>
<p>You can close this tab after pasting the value.</p>
</body>
</html>`, html.EscapeString(pasteValue))))
}
