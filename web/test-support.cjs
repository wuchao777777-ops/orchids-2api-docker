// Execute page assets with the same shared dependencies as the Go templates.
const nativeVM = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const asset = (name) => fs.readFileSync(path.join(__dirname, 'static/js', name), 'utf8');
module.exports = {
  ...nativeVM,
  runInContext(source, context, options) {
    if (!context.ConsoleUI) nativeVM.runInContext(asset('ui.js'), context);
    context.URL ||= URL;
    context.AbortController ||= AbortController;
    if (source.includes('const grokDeviceLogin =') && !context.DeviceAuthLogin) {
      nativeVM.runInContext(asset('device-auth.js'), context);
    }
    return nativeVM.runInContext(source, context, options);
  },
};
