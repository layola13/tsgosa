const A = [10, 20, 30] as const;
function main(): i32 {
  console.log(A[0] + A[2] + A.length);
  return 0;
}
