const F = true;
const A = `v${1.5}`;
const B = `w${F}`;
function C(): i32 {
  return 1;
}
const D = `x${C()}`;
function ua(): i32 {
  console.log(A);
  return 0;
}
function ub(): i32 {
  console.log(B);
  return 0;
}
function ud(): i32 {
  console.log(D);
  return 0;
}
function main(): i32 {
  return ua() + ub() + ud();
}
