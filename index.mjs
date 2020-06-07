import http from 'http';
import https from 'https';

const server = http.createServer();

const googleHostname = 'www.google.co.uk';

server.on('request', async (clientRequest, proxyResponse) => {
	try {
		// make request to server
		await new Promise((resolve, reject) => {
			const options = {
				hostname: googleHostname,
				headers: Object.assign(clientRequest.headers, {
					host: googleHostname
				}),
				method: clientRequest.method,
				path: clientRequest.url,
				setHost: false,
				// rejectUnauthorized: false
			};
			// make request to server
			const proxyRequest = https.request(options);
			proxyRequest.on('error', reject);
			proxyRequest.on('response', async serverResponse => {
				try {
					// sync headers and statusCode
					for (const header of Object.entries(serverResponse.headers)) {
						proxyResponse.setHeader(...header);
					}
					proxyResponse.statusCode = serverResponse.statusCode;
					// stream response to client
					serverResponse.on('readable', () => {
						let chunk;
						while(chunk = serverResponse.read()) {
							proxyResponse.write(chunk);
						}
					});
					serverResponse.on('end', resolve);
				} catch (error) {
					reject(error);
				}
			});
			clientRequest.pipe(proxyRequest);
			proxyRequest.end();
		});
	} catch (error) {
		proxyResponse.statusCode = 500;
		proxyResponse.write(error.stack);
		console.error(error);
	} finally {
		proxyResponse.end();
	}
});

const port = process.env.PORT || 50000;
server.listen(port, () => console.log('HTTP on port ' + port));
