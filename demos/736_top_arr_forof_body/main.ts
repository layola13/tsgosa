const A = [1, 2, 3];
function main(): i32 {
  let k = 0;
  for (const x of A) {
    k = k + 1;
    console.log(x * 10 + k);
  }
  return 0;
}
