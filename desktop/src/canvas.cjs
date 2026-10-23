'use strict';
function validateCanvas(input, snapshot = false) {
  const allowed = snapshot ? ['sessionID','surfaceID','revision'] : ['sessionID'];
  if (!input || typeof input !== 'object' || Array.isArray(input) || Object.keys(input).some(key => !allowed.includes(key)) || typeof input.sessionID !== 'string' || !/^[\w-]{1,128}$/.test(input.sessionID)) throw new Error('Invalid canvas request.');
  if (snapshot && (typeof input.surfaceID !== 'string' || !input.surfaceID || Buffer.byteLength(input.surfaceID)>256 || input.surfaceID.trim()!==input.surfaceID || /[\x00-\x1f\x7f]/.test(input.surfaceID) || !Number.isSafeInteger(input.revision) || input.revision<0)) throw new Error('Invalid canvas snapshot identity or revision.');
  return {...input};
}
module.exports = {validateCanvas};
