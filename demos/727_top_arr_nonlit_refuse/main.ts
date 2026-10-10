const S = ["a", "b"] as const;
const A = [1, 2], B = "x";
function main(): i32 {
  console.log(S.length + A[0]);
  return 0;
}
