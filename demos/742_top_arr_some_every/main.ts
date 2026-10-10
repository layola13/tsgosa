const A = [1, 2, 3];
function main(): i32 {
  console.log(A.some((x) => x > 2));
  console.log(A.every((x) => x > 5));
  console.log(A.findIndex((x) => x > 1));
  return 0;
}
