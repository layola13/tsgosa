const A: readonly number[] = [4, 5, 6];
function main(): i32 {
  console.log(A[1] + A.length);
  console.log(A.slice(2)[0]);
  return 0;
}
