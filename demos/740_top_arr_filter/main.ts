const A = [1, 2, 3, 4];
function main(): i32 {
  console.log(A.filter((x) => x > 2).length);
  console.log(A.filter((x) => x > 1)[0]);
  return 0;
}
