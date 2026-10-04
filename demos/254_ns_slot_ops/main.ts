namespace N {
  export let K: number = 7;
}
function main(): i32 {
  N.K += 3;
  N.K++;
  ++N.K;
  return N.K;
}
console.log(main());
