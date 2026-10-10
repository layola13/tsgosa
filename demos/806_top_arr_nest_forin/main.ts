const N = [[1, 2], [3, 4]];
function main(): i32 {
  let k = 0;
  for (const i in N) {
    k = k + i;
  }
  console.log(k);
  console.log(N[0][1] + N[1][1]);
  return 0;
}
