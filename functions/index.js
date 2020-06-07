const functions = require('firebase-functions');
const https = require('https');

const googleHostname = 'www.google.co.uk';

exports.index = functions.runWith({timeoutSeconds: 60}).https.onRequest(async (clientRequest, proxyResponse) => {
	try {
		// make request to server
		const {serverResponse, serverResponseBody} = await new Promise((resolve, reject) => {
			const options = {
				hostname: googleHostname,
				headers: Object.assign(clientRequest.headers, {
					host: googleHostname
				}),
				method: clientRequest.method,
				path: clientRequest.url,
				setHost: false,
				rejectUnauthorized: false
			};
			// strip firebase headers
			delete options.headers['x-forwarded-host'];
			delete options.headers['x-original-url'];
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
		proxyResponse.send();
	}
});
