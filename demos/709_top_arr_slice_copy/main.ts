const A = [1, 2, 3];
function main(): i32 {
  const b = A.slice();
  console.log(b.length + b[2]);
  console.log(A.slice(-1)[0]);
  return 0;
}
