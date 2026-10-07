namespace N {
  export let s: string = "x";
}
function main(): i32 {
  N.s = "yz";
  console.log(N.s.length);
  return N.s.length;
}
console.log(main());
