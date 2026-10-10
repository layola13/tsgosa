const A = [1, 2];
const E: i32[] = [];
function main(): i32 {
  if (A) {
    console.log(1);
  }
  console.log(!A);
  console.log(!E ? 9 : 8);
  console.log(A ? 1 : 2);
  return 0;
}
