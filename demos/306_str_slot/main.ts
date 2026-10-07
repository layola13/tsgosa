declare global {
  var gstr: string;
}
let tstr: string;
function main(): i32 {
  gstr = "hi";
  tstr = "yo!";
  console.log(gstr.length + tstr.length);
  return gstr.length + tstr.length;
}
console.log(main());
