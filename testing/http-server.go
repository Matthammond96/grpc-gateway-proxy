package TestServers

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/gin-gonic/gin"
)

func httpTestServer() {
	router := gin.Default()

	router.Use(func(c *gin.Context) {
    fmt.Println("Headers:", c.Request.Header)
    fmt.Println("Method:", c.Request.Method)
    bodyBytes, _ := c.GetRawData()
    fmt.Println("Body:", string(bodyBytes))
    // Restore the body for downstream handlers
    c.Request.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes))

		jsonData := []byte(`{"msg":"Response from 8080"}`)
    c.Data(http.StatusOK, "application/json", jsonData)
})


	router.Run("localhost:8080")
}