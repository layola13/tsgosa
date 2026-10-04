import T from "./timer";
function main(): i32 { return T.now() + T.diff(3); }
console.log(main());
