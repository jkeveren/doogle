const functions = require('firebase-functions');
const https = require('https');
const url = require('url');

const development = process.env.FUNCTIONS_EMULATOR === 'true';

const protocol = development ? 'http:' : 'https:';
const host = development ? 'localhost:50000' : 'doogle.keve.ren';

const replaceDomains = string => string.replace(/https?:\/\/([\w-]{0,63}\.){0,3}?google\.(com|ac|ad|ae|af|ag|ai|al|am|ao|ar|as|at|au|az|ba|bd|be|bf|bg|bh|bi|bj|bn|bo|br|bs|bt|bw|by|bz|ca|kh|cc|cd|cf|cat|cg|ch|ci|ck|cl|cm|cn|co|cr|cu|cv|cy|cz|de|dj|dk|dm|do|dz|ec|ee|eg|es|et|fi|fj|fm|fr|ga|ge|gf|gg|gh|gi|gl|gm|gp|gr|gt|gy|hk|hn|hr|ht|hu|id|iq|ie|il|im|in|io|is|it|je|jm|jo|jp|ke|ki|kg|kr|kw|kz|la|lb|lc|li|lk|ls|lt|lu|lv|ly|ma|md|me|mg|mk|ml|mm|mn|ms|mt|mu|mv|mw|mx|my|mz|na|ne|nf|ng|ni|nl|no|np|nr|nu|nz|om|pk|pa|pe|ph|pl|pg|pn|pr|ps|pt|py|qa|ro|rs|ru|rw|sa|sb|sc|se|sg|sh|si|sk|sl|sn|sm|so|st|sr|sv|td|tg|th|tj|tk|tl|tm|to|tn|tr|tt|tw|tz|ua|ug|uk|us|uy|uz|vc|ve|vg|vi|vn|vu|ws|za|zm|zw)/ig, protocol + '//$1' + host);

exports.index = functions.https.onRequest(async (clientRequest, proxyResponse) => {
	try {
		const clientRequestProtocol = clientRequest.headers['x-forwarded-proto'] ? clientRequest.headers['x-forwarded-proto'] + ':' : protocol;
		const clientRequestHost = clientRequest.headers['x-forwarded-host'] || host;
		const clientRequestPath = clientRequest.headers['x-forwarded-url'] || clientRequest.url;
		const clientRequestOrigin = clientRequestProtocol + '//' + clientRequestHost;
		const clientRequestHref = clientRequestOrigin + clientRequestPath;
		const clientRequestURL = new url.URL(clientRequestHref);
		const proxyRequestHost = clientRequestHost.replace(host, 'google.com');
		const proxyRequestHref = 'https://' + proxyRequestHost + clientRequestPath;
		const proxyRequestURL = new url.URL(proxyRequestHref);
		console.log('URL translation:', clientRequestHref, ' -> ', proxyRequestHref);
		// make request to server
		await new Promise((resolve, reject) => {
			const proxyRequestOptions = {
				headers: {},
				method: clientRequest.method,
				hostname: proxyRequestURL.hostname,
				path: proxyRequestURL.pathname,
			};
			for (const [key, value] of Object.entries(clientRequest.headers)) {
				// only forward non-x headers as these are usually firebase headers
				if (!/^(x-(forwarded|original))-/i.test(key)) {
					proxyRequestOptions.headers[key] = value;
				}
			}
			proxyRequestOptions.headers.host = proxyRequestHost;
			proxyRequestOptions.headers['accept-encoding'] = 'identity';
			if (clientRequestURL.pathname === '/search') {
				console.log(clientRequest.headers);
				console.log(proxyRequestOptions.headers);
			}
			// make request to server
			const proxyRequest = https.request(proxyRequestOptions);
			proxyRequest.on('error', reject);
			proxyRequest.on('response', async serverResponse => {
				try {
					// sync headers and statusCode
					for (let [key, value] of Object.entries(serverResponse.headers)) {
						if (key === 'location') {
							console.log('redirect:', value);
							value = replaceDomains(value);
							value = value.replace(/^https:/, protocol);
							console.log('redirect new value:', value);
						}
// 						if (
// 							!['content-length'].includes(key)
// 						) {
							proxyResponse.setHeader(key, value);
// 						}
					}
					proxyResponse.setHeader('access-control-allow-origin', clientRequest.headers.origin || '*');
					proxyResponse.setHeader('access-control-allow-credentials', true);
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
							let body = Buffer.concat(chunks).toString();
// 							body = replaceDomains(body);
// 							body = body.replace(/Google/g, 'Doogle');
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
		proxyResponse.end();
	}
});
