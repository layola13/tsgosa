const A = [1, 2];
const E: i32[] = [];
function main(): i32 {
  console.log(E.slice().length);
  console.log(E.indexOf(1));
  console.log(Array.isArray(A));
  console.log(Array.isArray(1));
  return 0;
}
