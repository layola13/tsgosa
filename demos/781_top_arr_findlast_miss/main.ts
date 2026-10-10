const A = [1, 2, 3];
function main(): i32 {
  console.log(A.findLast((x) => x > 9));
  console.log(A.findLastIndex((x) => x > 9));
  return 0;
}
