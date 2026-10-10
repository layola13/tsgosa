const A = [1, 2, 3];
function main(): i32 {
  console.log(A.map((x) => x + 1).length);
  console.log(A.map((x) => x * 10)[2]);
  return 0;
}
