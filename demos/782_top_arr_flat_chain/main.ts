const A = [1, 2, 3];
function main(): i32 {
  console.log(A.flat().slice(1)[0]);
  console.log(A.flat().flat()[1]);
  return 0;
}
