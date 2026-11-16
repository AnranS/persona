import { startGateway } from './smoke-gateway.mjs';
const gateway = await startGateway({dist:process.argv[2],mockPort:Number(process.argv[3])});
console.log(gateway.url);
await new Promise(() => {});
