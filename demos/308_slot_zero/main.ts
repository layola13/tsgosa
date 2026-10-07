namespace M {
  export let u: i32;
}
declare global {
  var w: i32;
}
function main(): i32 {
  console.log(M.u + w);
  return M.u + w;
}
console.log(main());
