const functions = require('firebase-functions');
const https = require('https');
const url = require('url');

const development = process.env.FUNCTIONS_EMULATOR === 'true';

const protocol = development ? 'http:' : 'https:';
const host = development ? 'localhost:50000' : 'doogle.keve.ren';

exports.index = functions.https.onRequest(async (clientRequest, proxyResponse) => {
	try {
		const clientRequestProtocol = clientRequest.headers['x-forwarded-proto'] ? clientRequest.headers['x-forwarded-proto'] + ':' : protocol;
		const clientRequestHost = clientRequest.headers['x-forwarded-host'] || host;
		const clientRequestPath = clientRequest.headers['x-forwarded-url'] || clientRequest.url;
		const clientRequestHref = clientRequestProtocol + '//' + clientRequestHost + clientRequestPath;
		const proxyRequestHost = clientRequestHost.replace(host, 'google.com');
		const proxyRequestHref = 'https://' + proxyRequestHost + clientRequestPath;
		console.log('URL translation:', clientRequestHref, ' -> ', proxyRequestHref);
		// make request to server
		await new Promise((resolve, reject) => {
			const proxyRequestOptions = {
				headers: {},
				method: clientRequest.method,
			};
			for (const [key, value] of Object.entries(clientRequest.headers)) {
				// only forward non-x headers as these are usually firebase headers
				if (!/^x-/i.test(key)) {
					proxyRequestOptions.headers[key] = value;
				}
			}
			proxyRequestOptions.headers.host = proxyRequestHost;
			// make request to server
			const proxyRequest = https.request(proxyRequestHref, proxyRequestOptions);
			proxyRequest.on('error', reject);
			proxyRequest.on('response', async serverResponse => {
				try {
					// sync headers and statusCode
					for (let [key, value] of Object.entries(serverResponse.headers)) {
						if (key === 'location') {
							console.log('redirect:', value);
							value = value.replace(/google\.[^/?#]{0,7}/i, host);
							value = value.replace(/^https:/, protocol);
							console.log('redirect new value:', value);
						}
						proxyResponse.setHeader(key, value);
					}
					proxyResponse.statusCode = serverResponse.statusCode;
					const responseContentType = serverResponse.headers['content-type'];
					if (['html'].some(type => responseContentType.includes(type))) {
						const chunks = [];
						serverResponse.on('readable', () => {
							let chunk;
							while(chunk = serverResponse.read()) {
								chunks.push(chunk);
							}
						});
						serverResponse.on('end', () => {
							let body = Buffer.concat(chunks);
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
	} catch (error) {
		proxyResponse.statusCode = 500;
		proxyResponse.write(error.stack);
		console.error(error);
	} finally {
		proxyResponse.send();
	}
});
