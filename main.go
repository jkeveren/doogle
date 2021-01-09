package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var workingDirectory string
var hostReg *regexp.Regexp
var proxyClient http.Client

func main() {
	// Define Globals
	var err error

	// Working Directory
	workingDirectory, err = os.Getwd()
	if err != nil {
		panic(err)
	}

	/*
		For parsing subdomains and extensions from specified hosts.
		This is domain specific in order to be able to parse two level extensions;
		hence the inclusion of specific name(s) in the name group.
		Note: "co.uk" is an extension not a TLD. Only the "uk" part is a TLD.
	*/
	hostReg = regexp.MustCompile("^(?P<subdomain>.+?\\.)?(?P<name>(d|g)oogle.|localhost)(?P<extension>[a-z-\\.]+)?(?P<port>:\\d{1,5})?$")

	// Client used to make requests to Google.
	proxyClient = http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Start Server
	port, err := getPort()
	if err != nil {
		panic(err)
	}
	fmt.Println("Starting HTTP server on port", port)
	panic(http.ListenAndServe(fmt.Sprintf(":%d", port), Handler{}))
}

func getPort() (int, error) {
	portString := os.Getenv("PORT")
	var port int
	if portString == "" {
		port = 42222
	} else {
		var err error
		port, err = strconv.Atoi(portString)
		if err != nil {
			return 0, err
		}
	}
	return port, nil
}

type Handler struct{}

func (h Handler) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	overridePath, isSafe := sanitizePath(filepath.Join(workingDirectory, "overrides", req.URL.Path))
	if !isSafe {
		res.WriteHeader(403)
		return
	}

	overrideAvailable, err := isOverrideAvailable(overridePath)
	if err != nil {
		serverError(res, err)
		return
	}

	if overrideAvailable {
		err = sendOverride(res, overridePath)
	} else {
		err = proxyRequest(res, req)
	}
	if err != nil {
		serverError(res, err)
		return
	}
}

func serverError(res http.ResponseWriter, err error) {
	res.WriteHeader(500)
	fmt.Println(err)
}

func sanitizePath(path string) (cleanedPath string, isSafe bool) {
	cleanedPath = filepath.Clean(path)
	isSafe = strings.HasPrefix(cleanedPath, workingDirectory)
	return
}

func isOverrideAvailable(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) || info.IsDir() {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

func sendOverride(res http.ResponseWriter, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := make([]byte, 1024)
	for {
		n, err := file.Read(buf)
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		_, err = res.Write(buf[:n])
		if err != nil {
			return err
		}
	}
	return nil
}

/*
	Proxies a request to Google.
	The request and response variable names can get confusing so here's adiagram:
	Client--[origReq]->Doogle--[proxyReq]->Google
	Client<-[origRes]--Doogle<-[proxyRes]--Google
*/

func proxyRequest(origRes http.ResponseWriter, origReq *http.Request) error {
	// forward requst method, URL and Body
	proxyReqURL, err := url.Parse(origReq.URL.String())
	if err != nil {
		return err
	}
	proxyReqURL.Scheme = "https"
	proxyReqURL.Host = hostReg.ReplaceAllString(origReq.Host, "${subdomain}google.com")
	proxyReq, err := http.NewRequest(origReq.Method, proxyReqURL.String(), origReq.Body)
	if err != nil {
		return err
	}

	// forward request headers
	for key, values := range origReq.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	/*
		Modify origin and referer headers.
		Innacurate for cross origin requests but there shouldn't be any where this matters
		Host header is set automatically by the proxyClient using the URL.
	*/
	if origin := origReq.Header.Get("origin"); origin != "" {
		newOrigin, err := replaceURLSubdomain(proxyReqURL, origin)
		if err != nil {
			return err
		}
		proxyReq.Header.Set("origin", newOrigin)
	}
	if referer := origReq.Header.Get("referer"); referer != "" {
		newReferer, err := replaceURLSubdomain(proxyReqURL, referer)
		if err != nil {
			return err
		}
		proxyReq.Header.Set("referer", newReferer)
	}

	// make request
	proxyRes, err := proxyClient.Do(proxyReq)
	if err != nil {
		if _, ok := err.(net.Error); ok && strings.HasSuffix(err.Error(), ": no such host") {
			origRes.WriteHeader(404)
			return nil
		} else {
			return err
		}
	}

	// forward reponse headers
	for key, values := range proxyRes.Header {
		for _, value := range values {
			origRes.Header().Add(key, value)
		}
	}

	// modify location header
	if location := proxyRes.Header.Get("location"); location != "" {
		locationURL, err := url.Parse(location)
		if err != nil {
			return err
		}
		locationURL.Scheme = "http" // TODO: check forwarded header

		hostWithoutSub := hostReg.ReplaceAllString(origReq.Host, "${name}${extension}${port}")
		regReplaceString := fmt.Sprintf("${subdomain}%s", hostWithoutSub)
		locationURL.Host = hostReg.ReplaceAllString(locationURL.Host, regReplaceString)
		origRes.Header().Set("Location", locationURL.String())
	}

	// forward status code
	origRes.WriteHeader(proxyRes.StatusCode)

	// forward response body
	buf := make([]byte, 1024)
	for {
		n, err := proxyRes.Body.Read(buf)
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		_, err = origRes.Write(buf[:n])
		if err != nil {
			return err
		}
	}

	return nil
}

// Replaces the subdomain of baseURL with the subdomain from subSrc
func replaceURLSubdomain(baseURL *url.URL, subSrc string) (string, error) {
	subSrcURL, err := url.Parse(subSrc)
	if err != nil {
		return "", err
	}
	subSrcURL.Scheme = baseURL.Scheme

	subdomain := hostReg.ReplaceAllString(subSrcURL.Host, "${subdomain}")
	regReplaceString := fmt.Sprintf("%s${name}${extension}${port}", subdomain)
	subSrcURL.Host = hostReg.ReplaceAllString(baseURL.Host, regReplaceString)

	return subSrcURL.String(), nil
}
