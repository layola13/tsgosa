const s = "top";
function f(): void {
  const s = "";
  console.log(!s ? 1 : 0);
}
f();
console.log(!s ? 1 : 0);
