const A = [3, 1, 2];
function main(): i32 {
  console.log(A.toSorted((a, b) => a - b)[0]);
  console.log(A.toSorted((a, b) => b - a)[0]);
  return 0;
}
