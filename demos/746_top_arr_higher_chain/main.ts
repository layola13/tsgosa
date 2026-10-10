const A = [1, 2, 3];
function main(): i32 {
  console.log(A.filter((x) => x > 0).map((x) => x * 2)[1]);
  return 0;
}
