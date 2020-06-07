import http from 'http';
import https from 'https';
import path from 'path';
import url from 'url';
import {promises as fs} from 'fs';

const server = http.createServer();

const deployedHostname = 'localhost:50000';

const googleTopHostname = 'google.co.uk';
const googleHostname = 'www.' + googleTopHostname;
const staticRoot = path.join(url.fileURLToPath(import.meta.url), '../static');

server.on('request', async (clientRequest, proxyResponse) => {
	try {
		// look for override file
		let file = null;
		try {
			file = await fs.readFile(path.join(staticRoot, clientRequest.url));
		} catch (error) {
			if (!['ENOENT', 'EISDIR'].includes(error.code)) {
				console.error(error);
			}
		}
		if (file) {
			// send override file
			proxyResponse.write(file);
		} else {
			const URLObject = new url.URL(clientRequest.url, 'https://' + clientRequest.headers.host);
			console.log(URLObject);
			if (['/search', '/imghp'].includes(URLObject.pathname)) {
				URLObject.searchParams.set('q', 'doogle');
			}
			// make request to server
			await new Promise((resolve, reject) => {
				const clientRequestHeadersCopy = Object.assign({}, clientRequest.headers);
				clientRequestHeadersCopy.host = googleHostname;
				clientRequestHeadersCopy['accept-encoding'] = 'identity';
				const options = {
					headers: clientRequestHeadersCopy,
					method: clientRequest.method,
					setHost: false,
				};
				// make request to server
				const proxyRequest = https.request(URLObject, options);
				proxyRequest.on('error', reject);
				proxyRequest.on('response', async serverResponse => {
					try {
						// sync headers and statusCode
						for (const header of Object.entries(serverResponse.headers)) {
							proxyResponse.setHeader(...header);
						}
						proxyResponse.statusCode = serverResponse.statusCode;
						// stream response to client
						const responseContentType = serverResponse.headers['content-type'];
						if (responseContentType && ['html'].some(type => responseContentType.includes(type))) {
							let chunks = [];
							serverResponse.on('readable', () => {
								let chunk;
								while(chunk = serverResponse.read()) {
									chunks.push(chunk);
								}
							});
							serverResponse.on('end', () => {
								let body = Buffer.concat(chunks);
								body = body.toString();
								body = body.replace(/\.google\.[^/"> ]{0,10}/ig, '.' + clientRequest.headers.host);
								body = body.replace(/google/ig, 'Doogle');
								proxyResponse.write(body);
								resolve();
							});
						} else {
							serverResponse.pipe(proxyResponse);
							serverResponse.on('end', resolve);
						}
					} catch (error) {
						reject(error);
					}
				});
				clientRequest.pipe(proxyRequest);
				proxyRequest.end();
			});
		}
	} catch (error) {
		proxyResponse.statusCode = 500;
		proxyResponse.write('Oops, this bit doesn\'t work.');
		console.error(error);
	} finally {
		proxyResponse.end();
	}
});

const port = process.env.PORT || 50000;
server.listen(port, () => console.log('HTTP on port ' + port));
