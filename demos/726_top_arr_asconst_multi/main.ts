const A = [1] as const, B = [2, 3] as const;
function main(): i32 {
  console.log(A[0] + B[1] + B.length);
  return 0;
}
