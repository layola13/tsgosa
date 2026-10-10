const A = [1, 2];
const B = [1, 2];
function eq(): i32 {
  console.log(A === B);
  return 0;
}
function st(): i32 {
  console.log(String(A));
  return 0;
}
function ad(): i32 {
  console.log(A + 1);
  return 0;
}
function main(): i32 {
  return eq() + st() + ad();
}
