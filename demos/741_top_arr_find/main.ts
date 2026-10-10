const A = [1, 2, 3];
function main(): i32 {
  console.log(A.find((x) => x > 1));
  console.log(A.find((x) => x > 9));
  return 0;
}
