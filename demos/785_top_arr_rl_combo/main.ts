const A = [5, 1, 4, 1, 5];
function main(): i32 {
  console.log(A.findLastIndex((x) => x == 1));
  console.log(A.lastIndexOf(5));
  return 0;
}
