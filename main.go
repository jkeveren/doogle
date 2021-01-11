package main

import (
	"fmt"
	"io"
	"io/ioutil"
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
var proxyClient http.Client

// Regexp
var hostReg *regexp.Regexp
var googleDomainReg *regexp.Regexp
var googleNameReg *regexp.Regexp
var beagleReg *regexp.Regexp

func main() {
	// define globals
	var err error

	// working directory
	workingDirectory, err = os.Getwd()
	// normalize for comparison in sanitizePath
	workingDirectory = strings.ToLower(workingDirectory)
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

	// https://gist.github.com/danielpunkass/2029185
	// https://shawnblanc.net/box/mint-unique-referrers-block-list.txt
	googleDomainReg = regexp.MustCompile("(?i)\\bgoogle\\.((com|[a-z]{2})(\\.[a-z]{2})?|(off\\.ai))")
	googleNameReg = regexp.MustCompile("(?i)google")
	beagleReg = regexp.MustCompile("(?i)beagle")

	// client used to make requests to Google
	proxyClient = http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// start server
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
	fmt.Println(req.Host + req.URL.String())
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

	fmt.Println(overrideAvailable)

	if overrideAvailable {
		if err = sendOverride(res, overridePath); err != nil {
			serverError(res, err)
			return
		}
	} else {
		proxyRequest(res, req)
	}
}

func serverError(res http.ResponseWriter, err error) {
	res.WriteHeader(500)
	fmt.Println(err)
}

func sanitizePath(path string) (cleanedPath string, isSafe bool) {
	cleanedPath = strings.ToLower(filepath.Clean(path))
	fmt.Println(cleanedPath, workingDirectory)
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
	_, err = io.Copy(res, file)
	if err != nil {
		return err
	}
	return nil
}

/*
	Proxies a request to Google.
	The request and response variable names can get confusing so here's adiagram:
	Client--[origReq]->Doogle--[proxyReq]->Google
	Client<-[origRes]--Doogle<-[proxyRes]--Google
*/

func proxyRequest(origRes http.ResponseWriter, origReq *http.Request) {
	// forward requst method, URL and Body
	proxyReqURL, err := url.Parse(origReq.URL.String())
	if err != nil {
		serverError(origRes, err)
		return
	}
	proxyReqURL.Scheme = "https"
	proxyReqURL.Host = hostReg.ReplaceAllString(origReq.Host, "${subdomain}google.com")
	isBeagle := strings.ToLower(origReq.URL.Path) == "/search"
	if isBeagle {
		proxyReqURL.RawQuery = "q=beagle&tbm=isch"
	}
	proxyReq, err := http.NewRequest(origReq.Method, proxyReqURL.String(), origReq.Body)
	if err != nil {
		serverError(origRes, err)
		return
	}

	// forward request headers
	for key, values := range origReq.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}
	// noAcceptEncoding := origReq.Header.Get("accept-encoding") == ""
	/*
		Do not forward accept-encoding header.
		The proxyClient Transport will transparently request compression and
		decompress if Accept-Encoding header is not explicitly added and
		DisableCompression is not enabled.
		https://golang.org/pkg/net/http/#Transport
	*/
	proxyReq.Header.Del("accept-encoding")

	/*
		Modify origin and referer headers.
		Innacurate for cross origin requests but it seems that there are none where
		it matters. Host header is set automatically by the proxyClient using the
		URL.
	*/
	if origin := origReq.Header.Get("origin"); origin != "" {
		newOrigin, err := replaceURLSubdomain(proxyReqURL, origin)
		if err != nil {
			serverError(origRes, err)
			return
		}
		proxyReq.Header.Set("origin", newOrigin)
	}
	if referer := origReq.Header.Get("referer"); referer != "" {
		newReferer, err := replaceURLSubdomain(proxyReqURL, referer)
		if err != nil {
			serverError(origRes, err)
			return
		}
		proxyReq.Header.Set("referer", newReferer)
	}

	// make request
	proxyRes, err := proxyClient.Do(proxyReq)
	if err != nil {
		if _, ok := err.(net.Error); ok && strings.HasSuffix(err.Error(), ": no such host") {
			origRes.WriteHeader(404)
			return
		} else {
			serverError(origRes, err)
			return
		}
	}

	// fmt.Println(proxyRes.Header)

	// forward reponse headers
	origReqBaseHost := getBaseHost(origReq.Host)
	for key, values := range proxyRes.Header {
		for _, value := range values {
			origRes.Header().Add(key, googleDomainReg.ReplaceAllString(value, origReqBaseHost))
		}
	}

	// modify location header
	if location := proxyRes.Header.Get("location"); location != "" {
		locationURL, err := url.Parse(location)
		if err != nil {
			serverError(origRes, err)
			return
		}
		locationURL.Scheme = "http" // TODO: check forwarded header

		repl := fmt.Sprintf("${subdomain}%s", origReqBaseHost)
		locationURL.Host = hostReg.ReplaceAllString(locationURL.Host, repl)
		origRes.Header().Set("Location", locationURL.String())
	}

	/*
		Forward response body.
		For specific mime types, replace any instance of Google or its domains with
		Doogle and its domains
	*/
	ct := strings.ToLower(proxyRes.Header.Get("content-type"))
	if strings.HasPrefix(ct, "text/html") {
		content, err := ioutil.ReadAll(proxyRes.Body)
		if err != nil {
			serverError(origRes, err)
			return
		}
		doogleBytes := []byte("Doogle")
		// replace Google with Doogle
		content = googleDomainReg.ReplaceAll(content, []byte(origReqBaseHost))
		content = googleNameReg.ReplaceAll(content, doogleBytes)
		// replace Beagle with Doogle
		if isBeagle {
			content = beagleReg.ReplaceAll(content, doogleBytes)
		}
		origRes.Header().Set("content-length", strconv.Itoa(len(content)))
		origRes.WriteHeader(proxyRes.StatusCode)
		_, err = origRes.Write(content)
		if err != nil {
			fmt.Println(err)
			return
		}
	} else {
		origRes.WriteHeader(proxyRes.StatusCode)
		_, err := io.Copy(origRes, proxyRes.Body)
		if err != nil {
			fmt.Println(err)
			return
		}
	}
}

// replaces the subdomain of baseURL with the subdomain from subSrc
func replaceURLSubdomain(baseURL *url.URL, subSrc string) (string, error) {
	subSrcURL, err := url.Parse(subSrc)
	if err != nil {
		return "", err
	}
	subSrcURL.Scheme = baseURL.Scheme

	subdomain := hostReg.ReplaceAllString(subSrcURL.Host, "${subdomain}")
	repl := fmt.Sprintf("%s${name}${extension}${port}", subdomain)
	subSrcURL.Host = hostReg.ReplaceAllString(baseURL.Host, repl)

	return subSrcURL.String(), nil
}

func getBaseHost(host string) string {
	return hostReg.ReplaceAllString(host, "${name}${extension}${port}")
}
