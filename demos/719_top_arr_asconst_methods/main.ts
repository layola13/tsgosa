const A = [1, 2, 3, 4] as const;
function main(): i32 {
  console.log(A.slice(1)[0]);
  console.log(A.indexOf(3));
  console.log(A.join("+"));
  return 0;
}
