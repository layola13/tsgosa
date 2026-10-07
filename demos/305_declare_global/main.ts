declare global {
  var gseed: i32;
  let gflag: i32;
}
function main(): i32 {
  gseed = 5;
  gflag = gseed + 1;
  console.log(gseed, gflag);
  return gseed + gflag;
}
console.log(main());
