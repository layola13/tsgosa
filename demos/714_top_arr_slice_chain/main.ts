const A = [1, 2, 3, 4];
function main(): i32 {
  console.log(A.slice(1).slice(1)[0]);
  console.log(A.slice(1, 9).length);
  return 0;
}
