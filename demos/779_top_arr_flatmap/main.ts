const A = [1, 2];
function main(): i32 {
  console.log(A.flatMap((x) => [x, x * 10]).length);
  console.log(A.flatMap((x) => [x, x * 10])[3]);
  return 0;
}
