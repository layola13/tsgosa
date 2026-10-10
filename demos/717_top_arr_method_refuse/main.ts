const A = [3, 1, 2];
function p(): i32 {
  console.log(A.pop());
  return 0;
}
function q(): i32 {
  const n = A.push(4);
  return n;
}
function r(): i32 {
  return A.splice(0, 1).length;
}
function t(): i32 {
  return A.sort()[0];
}
function main(): i32 {
  return p() + q() + r() + t();
}
