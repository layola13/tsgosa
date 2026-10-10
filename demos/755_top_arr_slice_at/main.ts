const A = [1, 2, 3];
function main(): i32 {
  console.log(A.slice(1).at(0));
  console.log(A.slice(1).at(-1));
  return 0;
}
