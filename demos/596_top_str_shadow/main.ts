const s = "top";
function f(): void {
  const s = "loc";
  console.log(s);
}
f();
console.log(s);
console.log(s.length);
