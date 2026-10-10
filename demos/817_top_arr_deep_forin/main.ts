const D = [[[1]], [[2, 3]]];
function main(): i32 {
  let k = 0;
  for (const i in D) {
    k = k + i;
  }
  console.log(k);
  console.log(D[1][0][1]);
  return 0;
}
