const A = [1, 2];
function main(): i32 {
  console.log(!A + 1);
  console.log(A.length + (!A ? 10 : 20));
  return 0;
}
