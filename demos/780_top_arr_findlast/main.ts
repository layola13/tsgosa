const A = [1, 2, 1, 3];
function main(): i32 {
  console.log(A.findLast((x) => x < 3));
  console.log(A.findLastIndex((x) => x < 3));
  return 0;
}
